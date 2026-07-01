package supervisor

// k8s.go — Kubernetes runtime backend for the supervisor.
//
// Model: one namespace per workspace ("platform-ws-<ws>"), one Pod and one
// Service per active service task.  The namespace is created lazily on the
// first EnsureService call; it is never automatically removed (workspace
// tenancy is longer-lived than individual tasks).
//
// Pod naming:    platform-ws-<ws>-<taskID>
// Service naming: platform-ws-<ws>-<taskID>
//
// Labels applied to every managed object:
//   platform.workspace = <ws>
//   platform.task      = <taskID>
//
// NOTE on deployment-time vs fake-tested: all object shapes are exercised
// against the fake clientset in k8s_test.go; in-cluster config and
// kubeconfig resolution in newK8s() require a live cluster and are therefore
// tested only at deployment time.

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/nk/platform/api/internal/store"
)

const (
	k8sNamespacePrefix = "platform-ws-"
	k8sContainerPort   = 3000
	k8sServicePort     = 80
	k8sPlatformLabel   = "platform"
	k8sWorkspaceLabel  = "platform.workspace"
	k8sTaskLabel       = "platform.task"
	k8sDefaultImage    = "node:22-alpine"
)

// K8s is the Kubernetes runtime backend.  It implements Client with one
// namespace per workspace and one pod+service per active service task.
type K8s struct {
	cs   kubernetes.Interface
	deps Deps
}

// newK8sWithClient constructs a K8s backend from an already-built clientset.
// Unexported; used internally.
func newK8sWithClient(cs kubernetes.Interface, deps Deps) *K8s {
	return &K8s{cs: cs, deps: deps}
}

// NewK8sWithClientForTest is the exported entry point for tests in package
// supervisor_test that need to inject a fake clientset. It returns Client
// so that tests are interface-bound rather than implementation-bound.
func NewK8sWithClientForTest(cs kubernetes.Interface, deps Deps) Client {
	return newK8sWithClient(cs, deps)
}

// newK8s constructs a K8s backend, building the clientset from in-cluster
// config (when running inside a Pod) or from $KUBECONFIG / ~/.kube/config.
// Requires a live cluster; deployment-time only.
func newK8s(deps Deps) (*K8s, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig on developer machines.
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = home + "/.kube/config"
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("supervisor/k8s: cannot build client config: %w", err)
		}
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("supervisor/k8s: cannot build clientset: %w", err)
	}
	return &K8s{cs: cs, deps: deps}, nil
}

// wsNamespace returns the namespace name for workspace ws.
func wsNamespace(ws string) string {
	return k8sNamespacePrefix + ws
}

// wsPodName returns the canonical pod name for a task in a workspace.
func wsPodName(ws, taskID string) string {
	return "platform-ws-" + ws + "-" + taskID
}

// wsServiceName returns the canonical service name for a task in a workspace.
func wsServiceName(ws, taskID string) string {
	return "platform-ws-" + ws + "-" + taskID
}

// k8sImage returns the container image to use; configurable via
// PLATFORM_K8S_IMAGE environment variable.
func k8sImage() string {
	if img := os.Getenv("PLATFORM_K8S_IMAGE"); img != "" {
		return img
	}
	return k8sDefaultImage
}

// ensureNamespace creates the namespace for ws if it does not already exist.
// It attempts Create directly and treats AlreadyExists as success, avoiding
// a Get-then-Create race under concurrent callers.
func (k *K8s) ensureNamespace(ctx context.Context, ws string) error {
	ns := wsNamespace(ws)
	_, err := k.cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
			Labels: map[string]string{
				k8sPlatformLabel:  "workspace",
				k8sWorkspaceLabel: ws,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("supervisor/k8s: create namespace %q: %w", ns, err)
	}
	return nil
}

// ---- Client interface -------------------------------------------------------

// EnsureService creates the namespace, Pod, and Service for task in workspace
// ws if they do not already exist.  Idempotent.
func (k *K8s) EnsureService(ws string, task *store.Task) error {
	ctx := context.Background()

	if err := k.ensureNamespace(ctx, ws); err != nil {
		return err
	}

	ns := wsNamespace(ws)
	pName := wsPodName(ws, task.ID)
	sName := wsServiceName(ws, task.ID)

	labels := map[string]string{
		k8sPlatformLabel:  "workspace",
		k8sWorkspaceLabel: ws,
		k8sTaskLabel:      task.ID,
	}

	// Create pod — tolerate AlreadyExists for idempotency under concurrent callers.
	_, err := k.cs.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pName,
			Namespace: ns,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "workflow",
					Image: k8sImage(),
					Ports: []corev1.ContainerPort{
						{ContainerPort: k8sContainerPort, Protocol: corev1.ProtocolTCP},
					},
					Env: []corev1.EnvVar{
						{Name: "PLATFORM_WORKSPACE", Value: ws},
						{Name: "PLATFORM_TASK", Value: task.ID},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyAlways,
		},
	}, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("supervisor/k8s: create pod for ws %q task %q: %w", ws, task.ID, err)
	}

	// Create service — tolerate AlreadyExists for idempotency under concurrent callers.
	_, err = k.cs.CoreV1().Services(ns).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sName,
			Namespace: ns,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				k8sWorkspaceLabel: ws,
				k8sTaskLabel:      task.ID,
			},
			Ports: []corev1.ServicePort{
				{
					Port:       k8sServicePort,
					TargetPort: intstr.FromInt(k8sContainerPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}, metav1.CreateOptions{})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("supervisor/k8s: create service for ws %q task %q: %w", ws, task.ID, err)
	}

	return nil
}

// StopService deletes the pod and service for taskID in workspace ws.
func (k *K8s) StopService(ws, taskID string) error {
	ctx := context.Background()
	ns := wsNamespace(ws)

	// Delete pod — ignore "not found".
	if err := k.cs.CoreV1().Pods(ns).Delete(ctx, wsPodName(ws, taskID), metav1.DeleteOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("supervisor/k8s: delete pod for ws %q task %q: %w", ws, taskID, err)
		}
	}

	// Delete service — ignore "not found".
	if err := k.cs.CoreV1().Services(ns).Delete(ctx, wsServiceName(ws, taskID), metav1.DeleteOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("supervisor/k8s: delete service for ws %q task %q: %w", ws, taskID, err)
		}
	}

	return nil
}

// Endpoint returns the Service's ClusterIP and port for a running task.
// ok is false when the service does not exist or has no ClusterIP assigned.
func (k *K8s) Endpoint(ws, taskID string) (base string, ports map[string]int, ok bool) {
	ctx := context.Background()
	ns := wsNamespace(ws)

	svc, err := k.cs.CoreV1().Services(ns).Get(ctx, wsServiceName(ws, taskID), metav1.GetOptions{})
	if err != nil || svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return "", nil, false
	}

	portMap := make(map[string]int, len(svc.Spec.Ports))
	for i, p := range svc.Spec.Ports {
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("port%d", i)
		}
		portMap[name] = int(p.Port)
	}

	return fmt.Sprintf("http://%s", svc.Spec.ClusterIP), portMap, true
}

// Health returns Up:true when a pod for the workspace exists and is Running
// with at least one Ready condition.
func (k *K8s) Health(ws string) Status {
	ctx := context.Background()
	ns := wsNamespace(ws)

	// List pods with the workspace label; any Running+Ready pod counts.
	pods, err := k.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: k8sWorkspaceLabel + "=" + ws,
	})
	if err != nil || len(pods.Items) == 0 {
		return Status{Up: false, Detail: fmt.Sprintf("no pods in namespace %q", ns)}
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		// Check Ready condition.
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return Status{Up: true}
			}
		}
	}

	return Status{
		Up:     false,
		Detail: fmt.Sprintf("no Running+Ready pod in namespace %q", ns),
	}
}

// ServiceRunning reports whether a pod exists and is in the Running phase for
// taskID in workspace ws.
func (k *K8s) ServiceRunning(ws, taskID string) bool {
	ctx := context.Background()
	ns := wsNamespace(ws)

	pod, err := k.cs.CoreV1().Pods(ns).Get(ctx, wsPodName(ws, taskID), metav1.GetOptions{})
	if err != nil {
		return false
	}
	return pod.Status.Phase == corev1.PodRunning
}

// StartRun creates a run record. For service tasks it delegates to
// EnsureService.  One-shot pod execution is a deployment-time concern.
func (k *K8s) StartRun(ws string, task *store.Task, trigger string) *store.Run {
	var run *store.Run
	if k.deps.Store != nil {
		run = k.deps.Store.CreateRun(task.ID, trigger)
	} else {
		now := time.Now()
		run = &store.Run{
			ID:          "run_k8s_" + task.ID,
			TaskID:      task.ID,
			WorkspaceID: ws,
			State:       store.RunQueued,
			Trigger:     trigger,
			StartedAt:   &now,
		}
	}

	if task.Kind == "service" {
		if err := k.EnsureService(ws, task); err != nil {
			run.State = store.RunFailed
			run.Error = err.Error()
		}
	}
	return run
}

// CancelRun signals cancellation of a run.  In the k8s backend this deletes
// the corresponding run pod.  Returns true if the pod existed and was deleted.
func (k *K8s) CancelRun(ws, runID string) bool {
	ctx := context.Background()
	ns := wsNamespace(ws)

	err := k.cs.CoreV1().Pods(ns).Delete(ctx, "run-"+runID, metav1.DeleteOptions{})
	return err == nil
}

// RestartService deletes the pod for a service task; the caller is expected to
// follow up with EnsureService to recreate it.
func (k *K8s) RestartService(ws, taskID string) error {
	ctx := context.Background()
	ns := wsNamespace(ws)

	err := k.cs.CoreV1().Pods(ns).Delete(ctx, wsPodName(ws, taskID), metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("supervisor/k8s: restart pod for ws %q task %q: %w", ws, taskID, err)
	}
	return nil
}

// BootServices starts all kind=service tasks across all workspaces by
// calling EnsureService for each one found in the store.
func (k *K8s) BootServices() error {
	if k.deps.Store == nil {
		return nil
	}
	tasks := k.deps.Store.ListServiceTasks()
	for _, t := range tasks {
		if err := k.EnsureService(t.WorkspaceID, t); err != nil {
			// Log but continue booting remaining services.
			_ = err
		}
	}
	return nil
}

// ReapIdle deletes pods for idle workspaces. A workspace is "warm" (preserved)
// if it has at least one non-disabled service task OR an in-flight
// (queued/running) run; only workspaces in neither set have their pods reaped.
// This ensures a run is never killed mid-execution.
//
// Policy (conservative):
//   - Build the warm set from ListServiceTasks() ∪ WorkspacesWithActiveRuns().
//   - List all namespaces with the label platform=workspace.
//   - For each namespace whose workspace is NOT warm, delete all pods in that
//     namespace (scale-to-zero). Services are left; they can be restarted by
//     EnsureService on next use.
//   - If the store is nil or the active-run query errors, skip reaping to be safe.
func (k *K8s) ReapIdle() error {
	if k.deps.Store == nil {
		return nil
	}

	// Build the warm set: workspaces with a service task ...
	warmWS := make(map[string]bool)
	for _, t := range k.deps.Store.ListServiceTasks() {
		warmWS[t.WorkspaceID] = true
	}
	// ... or with an in-flight run. On query error, skip reaping to be safe —
	// killing a workspace with an active run is worse than leaving one idle.
	activeRuns, err := k.deps.Store.WorkspacesWithActiveRuns()
	if err != nil {
		return nil
	}
	for ws := range activeRuns {
		warmWS[ws] = true
	}

	ctx := context.Background()

	// List all platform-managed namespaces.
	nsList, err := k.cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: k8sPlatformLabel + "=workspace",
	})
	if err != nil {
		return fmt.Errorf("supervisor/k8s: reap idle — list namespaces: %w", err)
	}

	for i := range nsList.Items {
		ns := &nsList.Items[i]
		ws := ns.Labels[k8sWorkspaceLabel]
		if ws == "" || warmWS[ws] {
			continue // workspace has a service task — keep pods alive
		}

		// Idle workspace: delete all pods in the namespace.
		pods, err := k.cs.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for _, pod := range pods.Items {
			_ = k.cs.CoreV1().Pods(ns.Name).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		}
	}

	return nil
}

