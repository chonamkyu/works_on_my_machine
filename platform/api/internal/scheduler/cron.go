package scheduler

import (
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/supervisor"
)

// Cron registers each task's enabled cron triggers with a single robfig/cron
// scheduler. The scheduler is started in main.go and re-syncs on task
// create/update events from the bus.
type Cron struct {
	store    *store.Memory
	dispatch supervisor.Client
	cron     *cron.Cron

	mu      sync.Mutex
	entries map[string]cron.EntryID // key = "<task_id>/<trigger_id>"
}

func NewCron(s *store.Memory, d supervisor.Client) *Cron {
	return &Cron{
		store:    s,
		dispatch: d,
		cron:     cron.New(cron.WithSeconds()),
		entries:  map[string]cron.EntryID{},
	}
}

func (c *Cron) Start() {
	c.Sync()
	c.cron.Start()
	log.Printf("cron scheduler started")
}

func (c *Cron) Stop() {
	c.cron.Stop()
}

// Sync rebuilds the schedule entries from the current task store. Idempotent
// — entries with unchanged spec stay; new ones are added; stale ones removed.
func (c *Cron) Sync() {
	c.mu.Lock()
	defer c.mu.Unlock()

	desired := map[string]string{}
	tasks := c.store.ListAllTasks()
	for _, t := range tasks {
		for _, tr := range t.Triggers {
			if tr.Type != "cron" || !tr.Enabled {
				continue
			}
			spec, ok := tr.Config["cron"].(string)
			if !ok || spec == "" {
				// Fallback: try "schedule" key (some prompts use that).
				if v, ok := tr.Config["schedule"].(string); ok {
					spec = v
				}
			}
			if spec == "" {
				continue
			}
			// robfig/cron with seconds expects 6 fields. If user gave 5, prepend "0".
			if len(splitFields(spec)) == 5 {
				spec = "0 " + spec
			}
			desired[t.ID+"/"+tr.ID] = spec
		}
	}

	// Remove stale.
	for key, id := range c.entries {
		if _, ok := desired[key]; !ok {
			c.cron.Remove(id)
			delete(c.entries, key)
		}
	}

	// Add new.
	for key, spec := range desired {
		if _, ok := c.entries[key]; ok {
			continue
		}
		taskID, triggerID := splitKey(key)
		task, err := c.store.GetTask(taskID)
		if err != nil {
			continue
		}
		id, err := c.cron.AddFunc(spec, c.fireFor(task.ID, triggerID, spec))
		if err != nil {
			log.Printf("cron: bad spec %q for %s: %v", spec, key, err)
			continue
		}
		c.entries[key] = id
		log.Printf("cron: scheduled %s @ %q", key, spec)
	}
}

func (c *Cron) fireFor(taskID, triggerID, spec string) func() {
	return func() {
		task, err := c.store.GetTask(taskID)
		if err != nil {
			return
		}
		c.dispatch.StartRun(task.WorkspaceID, task, "cron:"+triggerID)

		// Record health: compute next fire time from the cron spec directly so
		// the value is accurate regardless of when robfig/cron recalculates the
		// entry's internal Next field. Best-effort — log on failure, never crash.
		nextFireAt := nextFromSpec(spec)
		if err := c.store.RecordTriggerFire(taskID, triggerID, task.WorkspaceID, nextFireAt, ""); err != nil {
			log.Printf("cron: record trigger health for %s/%s: %v", taskID, triggerID, err)
		}
	}
}

// nextFromSpec parses spec with the same options as the scheduler (seconds
// field enabled) and returns the next scheduled time after now, or nil if
// parsing fails.
func nextFromSpec(spec string) *time.Time {
	parser := cron.NewParser(
		cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
	sched, err := parser.Parse(spec)
	if err != nil {
		return nil
	}
	next := sched.Next(time.Now())
	if next.IsZero() {
		return nil
	}
	return &next
}

func splitFields(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func splitKey(k string) (string, string) {
	for i := len(k) - 1; i >= 0; i-- {
		if k[i] == '/' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// PollLoop runs Sync every interval to pick up task changes. Cheap and good
// enough until LISTEN/NOTIFY is wired.
func (c *Cron) PollLoop(interval time.Duration, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.Sync()
		}
	}
}
