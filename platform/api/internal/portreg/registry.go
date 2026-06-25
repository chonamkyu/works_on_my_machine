// Package portreg keeps the TaskID <-> localhost-port mapping used by the
// dashboard reverse-proxy.
//
// Each `kind=service` workflow can expose multiple named HTTP surfaces (UI,
// API, websocket, …). The registry allocates one free loopback port per
// declared TaskPort and the control plane reverse-proxies
// `/d/<task_id>/<name>/*` to each. The "ui" role is also reachable bare at
// `/d/<task_id>/` so the iframe default URL stays clean.
package portreg

import (
	"net"
	"sync"
)

// Binding is one allocated port for a (task, name) pair.
type Binding struct {
	TaskID string
	Name   string // e.g. "ui", "api", "ws". Default name is "ui".
	Role   string // "ui" | "api" | "other"
	Port   int
}

// Registry is thread-safe. The zero value is NOT usable; call New().
type Registry struct {
	mu sync.RWMutex
	// byTask: taskID -> ( name -> *Binding )
	byTask map[string]map[string]*Binding
}

func New() *Registry {
	return &Registry{byTask: map[string]map[string]*Binding{}}
}

// AllocateNamed reserves a free loopback port for the given (task, name).
// Idempotent: if the same name was already allocated for this task and the
// port is still free, we hand back the existing one so dashboard URLs are
// stable across supervisor restarts.
func (r *Registry) AllocateNamed(taskID, name, role string) (int, error) {
	if name == "" {
		name = "ui"
	}
	r.mu.Lock()
	bag := r.byTask[taskID]
	if bag != nil {
		if b, ok := bag[name]; ok && isPortFree(b.Port) {
			r.mu.Unlock()
			return b.Port, nil
		}
	}
	port, err := pickFreePort()
	if err != nil {
		r.mu.Unlock()
		return 0, err
	}
	if bag == nil {
		bag = map[string]*Binding{}
		r.byTask[taskID] = bag
	}
	bag[name] = &Binding{TaskID: taskID, Name: name, Role: role, Port: port}
	r.mu.Unlock()
	return port, nil
}

// Allocate is the single-port back-compat wrapper. Equivalent to allocating
// a binding named "ui" with role "ui".
func (r *Registry) Allocate(taskID string) (int, error) {
	return r.AllocateNamed(taskID, "ui", "ui")
}

// Lookup returns the port of the "ui" binding for the task (back-compat).
func (r *Registry) Lookup(taskID string) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if bag, ok := r.byTask[taskID]; ok {
		if b, ok := bag["ui"]; ok {
			return b.Port, true
		}
		// No "ui" binding — fall back to whatever has role="ui" or the first
		// binding so /d/<task>/ stays useful even if names diverge.
		for _, b := range bag {
			if b.Role == "ui" {
				return b.Port, true
			}
		}
		for _, b := range bag {
			return b.Port, true
		}
	}
	return 0, false
}

// LookupNamed returns the port for a specific binding name on a task.
func (r *Registry) LookupNamed(taskID, name string) (*Binding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if bag, ok := r.byTask[taskID]; ok {
		if b, ok := bag[name]; ok {
			return b, true
		}
	}
	return nil, false
}

// ListForTask returns all bindings for a task — used by the panel's port-map
// view and the runner when injecting `PLATFORM_PORT_<NAME>` env vars.
func (r *Registry) ListForTask(taskID string) []Binding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Binding{}
	if bag, ok := r.byTask[taskID]; ok {
		for _, b := range bag {
			out = append(out, *b)
		}
	}
	return out
}

// Release frees every binding for a task. Called when the supervisor stops
// the daemon or the task is archived.
func (r *Registry) Release(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byTask, taskID)
}

// All returns a snapshot of all bindings — admin endpoints only.
func (r *Registry) All() []Binding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Binding{}
	for _, bag := range r.byTask {
		for _, b := range bag {
			out = append(out, *b)
		}
	}
	return out
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func isPortFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
