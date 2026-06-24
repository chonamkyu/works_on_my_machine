// Package portreg keeps the TaskID <-> localhost-port mapping used by the
// dashboard reverse-proxy. Each `kind=service` workflow gets a unique loopback
// port at spawn time, listens on it, and the control plane forwards
// `/d/<task_id>/*` requests to that port.
package portreg

import (
	"net"
	"sync"
)

type entry struct {
	Port    int
	TaskID  string
	Pinned  bool // future use — pre-allocated stable ports
}

// Registry is thread-safe. The zero value is NOT usable; call New().
type Registry struct {
	mu    sync.RWMutex
	byID  map[string]*entry
	byPort map[int]string
}

func New() *Registry {
	return &Registry{
		byID:   map[string]*entry{},
		byPort: map[int]string{},
	}
}

// Allocate reserves a free loopback port for the given task and remembers it.
// If a port was already allocated for this task (e.g. supervisor restart) we
// keep the same port so the dashboard URL stays stable.
func (r *Registry) Allocate(taskID string) (int, error) {
	r.mu.Lock()
	if e, ok := r.byID[taskID]; ok {
		r.mu.Unlock()
		// Make sure the port is still free (a previous child holding it should
		// have died); if not, fall through to a fresh allocation.
		if isPortFree(e.Port) {
			return e.Port, nil
		}
		r.mu.Lock()
	}
	port, err := pickFreePort()
	if err != nil {
		r.mu.Unlock()
		return 0, err
	}
	r.byID[taskID] = &entry{Port: port, TaskID: taskID}
	r.byPort[port] = taskID
	r.mu.Unlock()
	return port, nil
}

// Lookup returns the port previously allocated for taskID, if any.
func (r *Registry) Lookup(taskID string) (int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byID[taskID]
	if !ok {
		return 0, false
	}
	return e.Port, true
}

// Release frees the port association for taskID. The child holding the port
// should be dead first (otherwise the port stays bound at the OS layer).
func (r *Registry) Release(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.byID[taskID]; ok {
		delete(r.byPort, e.Port)
		delete(r.byID, taskID)
	}
}

// All returns a snapshot of all current bindings — used by debug endpoints.
func (r *Registry) All() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int, len(r.byID))
	for id, e := range r.byID {
		out[id] = e.Port
	}
	return out
}

// pickFreePort asks the kernel for an unused loopback port via Listen(":0").
// Releasing immediately is racey (another process could snatch it before the
// workflow binds), but in practice the workflow child is the only thing
// rushing to claim it and binding usually wins.
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
