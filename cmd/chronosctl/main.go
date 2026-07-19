// Command chronosctl is a small operator/demo CLI for starting and inspecting
// workflows over gRPC.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/sdk/client"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	if err := dispatch(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dispatch(cmd string, args []string) error {
	switch cmd {
	case "start":
		return cmdStart(args)
	case "describe":
		return cmdDescribe(args)
	case "history":
		return cmdHistory(args)
	case "list":
		return cmdList(args)
	case "await":
		return cmdAwait(args)
	case "signal":
		return cmdSignal(args)
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `chronosctl <command> [flags]

Commands:
  start      Start a workflow
  describe   Show execution status
  history    Print the event history
  list       List executions (optionally by status)
  await      Block until an execution reaches a status
  signal     Send a signal to a running workflow`)
}

func dial(addr string) (*client.Client, error) {
	if addr == "" {
		addr = envOr("CHRONOS_SERVER", "localhost:7233")
	}
	return client.Dial(addr)
}

func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	addr := fs.String("server", "", "control-plane address")
	id := fs.String("id", "", "workflow id (required)")
	wfType := fs.String("type", "OrderFulfillment", "workflow type")
	queue := fs.String("queue", "default", "task queue")
	input := fs.String("input", "", "JSON input payload")
	_ = fs.Parse(args)
	if *id == "" {
		return fmt.Errorf("-id is required")
	}
	c, err := dial(*addr)
	if err != nil {
		return err
	}
	defer c.Close()

	var payload any
	if *input != "" {
		if err := json.Unmarshal([]byte(*input), &payload); err != nil {
			return fmt.Errorf("parse -input: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.StartWorkflow(ctx, client.StartWorkflowOptions{
		ID: *id, WorkflowType: *wfType, TaskQueue: *queue, Input: payload,
	})
	if err != nil {
		return err
	}
	fmt.Printf("workflow_id=%s run_id=%s already_started=%v\n", res.WorkflowID, res.RunID, res.AlreadyStarted)
	return nil
}

func cmdDescribe(args []string) error {
	fs := flag.NewFlagSet("describe", flag.ExitOnError)
	addr := fs.String("server", "", "control-plane address")
	id := fs.String("id", "", "workflow id")
	run := fs.String("run", "", "run id")
	_ = fs.Parse(args)
	c, err := dial(*addr)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e, err := c.DescribeWorkflow(ctx, *id, *run)
	if err != nil {
		return err
	}
	fmt.Printf("workflow_id=%s\nrun_id=%s\ntype=%s\nstatus=%s\nhistory_length=%d\n",
		e.WorkflowId, e.RunId, e.WorkflowType, statusName(e.Status), e.HistoryLength)
	return nil
}

func cmdHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	addr := fs.String("server", "", "control-plane address")
	id := fs.String("id", "", "workflow id")
	run := fs.String("run", "", "run id")
	_ = fs.Parse(args)
	c, err := dial(*addr)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := c.GetHistory(ctx, *id, *run)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tDETAIL")
	for _, e := range events {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", e.EventId, eventTypeName(e.EventType), eventDetail(e))
	}
	return tw.Flush()
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	addr := fs.String("server", "", "control-plane address")
	statusFlag := fs.String("status", "", "filter: running|completed|failed")
	_ = fs.Parse(args)
	c, err := dial(*addr)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	execs, err := c.ListWorkflows(ctx, parseStatus(*statusFlag), 100)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "WORKFLOW ID\tTYPE\tSTATUS\tEVENTS")
	for _, e := range execs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", e.WorkflowId, e.WorkflowType, statusName(e.Status), e.HistoryLength)
	}
	return tw.Flush()
}

func cmdAwait(args []string) error {
	fs := flag.NewFlagSet("await", flag.ExitOnError)
	addr := fs.String("server", "", "control-plane address")
	id := fs.String("id", "", "workflow id")
	run := fs.String("run", "", "run id")
	want := fs.String("status", "completed", "status to wait for")
	timeout := fs.Duration("timeout", 60*time.Second, "max wait")
	_ = fs.Parse(args)
	c, err := dial(*addr)
	if err != nil {
		return err
	}
	defer c.Close()
	deadline := time.Now().Add(*timeout)
	target := parseStatus(*want)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e, err := c.DescribeWorkflow(ctx, *id, *run)
		cancel()
		if err == nil && e.Status == target {
			fmt.Printf("reached status=%s history_length=%d\n", statusName(e.Status), e.HistoryLength)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for status %s", *want)
}

func cmdSignal(args []string) error {
	fs := flag.NewFlagSet("signal", flag.ExitOnError)
	addr := fs.String("server", "", "control-plane address")
	id := fs.String("id", "", "workflow id")
	run := fs.String("run", "", "run id")
	name := fs.String("name", "", "signal name")
	input := fs.String("input", "", "JSON input payload")
	_ = fs.Parse(args)
	c, err := dial(*addr)
	if err != nil {
		return err
	}
	defer c.Close()
	var payload any
	if *input != "" {
		if err := json.Unmarshal([]byte(*input), &payload); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.SignalWorkflow(ctx, *id, *run, *name, payload); err != nil {
		return err
	}
	fmt.Println("signaled")
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseStatus(s string) commonv1.WorkflowStatus {
	switch strings.ToLower(s) {
	case "running":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING
	case "completed":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED
	case "failed":
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_FAILED
	default:
		return commonv1.WorkflowStatus_WORKFLOW_STATUS_UNSPECIFIED
	}
}

func statusName(s commonv1.WorkflowStatus) string {
	return strings.TrimPrefix(strings.ToLower(s.String()), "workflow_status_")
}

func eventTypeName(t commonv1.EventType) string {
	return strings.TrimPrefix(t.String(), "EVENT_TYPE_")
}

func eventDetail(e *commonv1.HistoryEvent) string {
	switch a := e.Attributes.(type) {
	case *commonv1.HistoryEvent_WorkflowExecutionStarted:
		return "type=" + a.WorkflowExecutionStarted.WorkflowType
	case *commonv1.HistoryEvent_ActivityTaskScheduled:
		return a.ActivityTaskScheduled.ActivityType + " id=" + a.ActivityTaskScheduled.ActivityId
	case *commonv1.HistoryEvent_ActivityTaskCompleted:
		return fmt.Sprintf("scheduled=%d result=%s", a.ActivityTaskCompleted.ScheduledEventId, truncate(a.ActivityTaskCompleted.Result))
	case *commonv1.HistoryEvent_ActivityTaskFailed:
		return fmt.Sprintf("scheduled=%d error=%s", a.ActivityTaskFailed.ScheduledEventId, a.ActivityTaskFailed.Failure.GetMessage())
	case *commonv1.HistoryEvent_ActivityTaskRetryScheduled:
		return fmt.Sprintf("scheduled=%d attempt=%d", a.ActivityTaskRetryScheduled.ScheduledEventId, a.ActivityTaskRetryScheduled.Attempt)
	case *commonv1.HistoryEvent_TimerStarted:
		return "timer=" + a.TimerStarted.TimerId
	case *commonv1.HistoryEvent_TimerFired:
		return "timer=" + a.TimerFired.TimerId
	case *commonv1.HistoryEvent_WorkflowExecutionSignaled:
		return "signal=" + a.WorkflowExecutionSignaled.SignalName
	case *commonv1.HistoryEvent_WorkflowExecutionCompleted:
		return "result=" + truncate(a.WorkflowExecutionCompleted.Result)
	case *commonv1.HistoryEvent_WorkflowExecutionFailed:
		return "error=" + a.WorkflowExecutionFailed.Failure.GetMessage()
	default:
		return ""
	}
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
