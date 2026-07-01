package supervisor_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/supervisor"
)

// newK8sClient builds a K8s backend backed by a fake clientset.
// Accesses the exported NewK8sWithClient factory; we use the package-level
// unexported constructor via the exported test helper in the supervisor package.
// Since we are in package supervisor_test we access it via the exported
// constructor defined below.
func newTestK8s(t *testing.T) supervisor.Client {
	t.Helper()
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	return supervisor.NewK8sWithClientForTest(cs, deps)
}

// svcTask returns a minimal service-kind Task.
func svcTask(ws, id string) *store.Task {
	return &store.Task{
		ID:          id,
		WorkspaceID: ws,
		Kind:        "service",
	}
}

// TestK8sEnsureService_CreatesNamespacePodService verifies that EnsureService
// creates the expected namespace, pod, and service objects in the fake
// clientset.
func TestK8sEnsureService_CreatesNamespacePodService(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	k := supervisor.NewK8sWithClientForTest(cs, deps)

	ws := "wsA"
	task := svcTask(ws, "task-1")

	if err := k.EnsureService(ws, task); err != nil {
		t.Fatalf("EnsureService: %v", err)
	}

	expectedNS := "platform-ws-" + ws

	// Assert namespace exists.
	ns, err := cs.CoreV1().Namespaces().Get(t.Context(), expectedNS, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace %q not found: %v", expectedNS, err)
	}
	if ns.Name != expectedNS {
		t.Errorf("namespace name = %q, want %q", ns.Name, expectedNS)
	}

	// Assert pod exists with correct labels.
	pods, err := cs.CoreV1().Pods(expectedNS).List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Fatalf("no pods found in namespace %q", expectedNS)
	}
	pod := pods.Items[0]
	if pod.Labels["platform.workspace"] != ws {
		t.Errorf("pod label platform.workspace = %q, want %q", pod.Labels["platform.workspace"], ws)
	}
	if pod.Labels["platform.task"] != task.ID {
		t.Errorf("pod label platform.task = %q, want %q", pod.Labels["platform.task"], task.ID)
	}

	// Assert service exists.
	svcs, err := cs.CoreV1().Services(expectedNS).List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list services: %v", err)
	}
	if len(svcs.Items) == 0 {
		t.Fatalf("no services found in namespace %q", expectedNS)
	}
}

// TestK8sEnsureService_Idempotent verifies that calling EnsureService twice
// does not return an error.
func TestK8sEnsureService_Idempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	k := supervisor.NewK8sWithClientForTest(cs, deps)

	ws := "wsA"
	task := svcTask(ws, "task-1")

	if err := k.EnsureService(ws, task); err != nil {
		t.Fatalf("first EnsureService: %v", err)
	}
	if err := k.EnsureService(ws, task); err != nil {
		t.Errorf("second EnsureService (idempotent check) failed: %v", err)
	}
}

// TestK8sHealth_UpWhenPodRunningAndReady verifies that Health returns Up:true
// when a pod with Running phase + Ready condition exists.
func TestK8sHealth_UpWhenPodRunningAndReady(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	k := supervisor.NewK8sWithClientForTest(cs, deps)

	ws := "wsB"
	task := svcTask(ws, "task-2")

	// Create resources.
	if err := k.EnsureService(ws, task); err != nil {
		t.Fatalf("EnsureService: %v", err)
	}

	ns := "platform-ws-" + ws

	// Find the pod created by EnsureService.
	pods, err := cs.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("pod not found: %v", err)
	}
	podName := pods.Items[0].Name

	// Update the pod status to Running + Ready.
	pod := pods.Items[0].DeepCopy()
	pod.Status.Phase = corev1.PodRunning
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		},
	}
	_, err = cs.CoreV1().Pods(ns).UpdateStatus(t.Context(), pod, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateStatus for pod %q: %v", podName, err)
	}

	// Health should be Up.
	status := k.Health(ws)
	if !status.Up {
		t.Errorf("Health(%q) = Up:false, want true; detail: %q", ws, status.Detail)
	}
}

// TestK8sHealth_DownWhenNoPod verifies that Health returns Up:false when no
// pod exists for the workspace.
func TestK8sHealth_DownWhenNoPod(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	k := supervisor.NewK8sWithClientForTest(cs, deps)

	status := k.Health("nonexistent-ws")
	if status.Up {
		t.Error("Health should return Up:false for workspace with no pods")
	}
}

// TestK8sHealth_DownWhenPodNotRunning verifies that Health returns Up:false
// when the pod exists but is in Pending phase (not Running+Ready).
func TestK8sHealth_DownWhenPodNotRunning(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	k := supervisor.NewK8sWithClientForTest(cs, deps)

	ws := "wsC"
	task := svcTask(ws, "task-3")

	if err := k.EnsureService(ws, task); err != nil {
		t.Fatalf("EnsureService: %v", err)
	}
	// Pod is Pending by default in the fake clientset (no status update).
	status := k.Health(ws)
	if status.Up {
		t.Error("Health should return Up:false when pod is not Running+Ready")
	}
}

// TestK8sStopService_RemovesPod verifies that StopService deletes the pod
// (and service) from the fake clientset.
func TestK8sStopService_RemovesPod(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	k := supervisor.NewK8sWithClientForTest(cs, deps)

	ws := "wsD"
	task := svcTask(ws, "task-4")

	if err := k.EnsureService(ws, task); err != nil {
		t.Fatalf("EnsureService: %v", err)
	}

	ns := "platform-ws-" + ws

	// Confirm pod exists before stop.
	pods, err := cs.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{})
	if err != nil || len(pods.Items) == 0 {
		t.Fatalf("pod not found before StopService: %v", err)
	}

	if err := k.StopService(ws, task.ID); err != nil {
		t.Fatalf("StopService: %v", err)
	}

	// Pod must be gone.
	podsAfter, err := cs.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods after StopService: %v", err)
	}
	if len(podsAfter.Items) != 0 {
		t.Errorf("pod still present after StopService; found %d pods", len(podsAfter.Items))
	}
}

// TestK8sStopService_Idempotent verifies that StopService on a non-existent
// pod does not return an error.
func TestK8sStopService_Idempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	deps := supervisor.Deps{}
	k := supervisor.NewK8sWithClientForTest(cs, deps)

	if err := k.StopService("wsE", "no-such-task"); err != nil {
		t.Errorf("StopService on non-existent pod should not error: %v", err)
	}
}

// TestK8sNewClientFactory verifies that NewClient("k8s", deps) no longer
// returns errUnsupported — it attempts to build the real clientset.
// On a machine with no kubeconfig this will error, but the error must NOT be
// the old "not implemented yet" sentinel.
func TestK8sNewClientFactory(t *testing.T) {
	deps := supervisor.Deps{}
	_, err := supervisor.NewClient("k8s", deps)
	if err != nil {
		// The error is expected (no kubeconfig on CI), but must not be the
		// "not implemented" stub error.
		if err.Error() == `supervisor: runtime "k8s" not implemented yet` {
			t.Errorf("NewClient(\"k8s\") still returns not-implemented stub: %v", err)
		}
		// Any other error (e.g. cannot build client config) is acceptable.
		t.Logf("NewClient(\"k8s\") returned expected config error (no kubeconfig): %v", err)
	}
}
