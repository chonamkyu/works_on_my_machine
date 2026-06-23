package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

const defaultAPI = "http://localhost:8787"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "tasks":
		listTasks(args)
	case "run":
		runTask(args)
	case "logs":
		showLogs(args)
	case "dev":
		fmt.Fprintln(os.Stderr, "use ./scripts/dev.sh for tmux dev session (Phase 0)")
		os.Exit(1)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: platform <command> [args]

commands:
  tasks               list tasks
  run <task_id>       trigger a manual run
  logs <run_id>       print run logs (one-shot)
  dev                 launch tmux dev session (delegates to scripts/dev.sh)

env:
  PLATFORM_API        base URL (default: %s)
`, defaultAPI)
}

func apiBase() string {
	if v := os.Getenv("PLATFORM_API"); v != "" {
		return v
	}
	return defaultAPI
}

func listTasks(_ []string) {
	resp, err := http.Get(apiBase() + "/v1/tasks")
	fatalOnErr(err)
	defer resp.Body.Close()
	var body struct {
		Tasks []struct {
			ID, Name, Status string
		} `json:"tasks"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	for _, t := range body.Tasks {
		fmt.Printf("%-30s  %-12s  %s\n", t.ID, t.Status, t.Name)
	}
}

func runTask(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: platform run <task_id>")
		os.Exit(1)
	}
	id := fs.Arg(0)
	resp, err := http.Post(apiBase()+"/v1/tasks/"+id+"/runs", "application/json", bytes.NewReader([]byte("{}")))
	fatalOnErr(err)
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

func showLogs(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: platform logs <run_id>")
		os.Exit(1)
	}
	resp, err := http.Get(apiBase() + "/v1/runs/" + args[0])
	fatalOnErr(err)
	defer resp.Body.Close()
	var body struct {
		Logs []struct {
			Stream, Text string
		} `json:"logs"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	for _, l := range body.Logs {
		fmt.Printf("[%s] %s\n", l.Stream, l.Text)
	}
}

func fatalOnErr(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
