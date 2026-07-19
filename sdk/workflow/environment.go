package workflow

import (
	"fmt"
	"io"
	"log/slog"
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/sdk/converter"
	"google.golang.org/protobuf/types/known/durationpb"
)

// DecisionResult is the output of processing one workflow task: the new
// commands the workflow decided on, and whether the workflow terminated.
type DecisionResult struct {
	Commands  []*commonv1.Command
	Completed bool
}

// blockedSignal is panicked to unwind workflow execution when it reaches a
// point that cannot make progress until more history arrives. Because a
// blocked workflow never resumes within the same decision, unwinding the stack
// (rather than parking a goroutine) is sufficient and keeps replay allocation
// free of scheduler machinery.
type blockedSignal struct{}

// activityState is the replay projection of one scheduled activity.
type activityState struct {
	activityType string
	completed    bool
	failed       bool
	result       []byte
	failure      *commonv1.Failure
}

// timerState is the replay projection of one started timer.
type timerState struct {
	fired bool
}

type signalRecord struct {
	name     string
	input    []byte
	consumed bool
}

// environment holds all replay state for a single workflow task.
type environment struct {
	input      []byte
	now        time.Time
	taskQueue  string
	terminated bool

	activities []*activityState
	timers     []*timerState
	signals    []*signalRecord

	activitySeq int
	timerSeq    int

	newCommands []*commonv1.Command
	replaying   bool

	logger *slog.Logger
}

// Execute runs the workflow definition against its history and returns the
// commands the workflow produces for this decision. It is pure with respect to
// history: identical history always yields identical commands.
func Execute(def Definition, taskQueue string, history []*commonv1.HistoryEvent, logger *slog.Logger) (result DecisionResult, err error) {
	env := newEnvironment(taskQueue, history, logger)

	// If the workflow already closed in history, there is nothing to decide.
	if env.terminated {
		return DecisionResult{Completed: true}, nil
	}

	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(blockedSignal); ok {
				// Workflow yielded awaiting more history; emit accumulated commands.
				result = DecisionResult{Commands: env.newCommands}
				err = nil
				return
			}
			// An application panic terminates the workflow with failure.
			result = DecisionResult{
				Commands: append(env.newCommands, failWorkflowCommand(&commonv1.Failure{
					Message: fmt.Sprintf("workflow panic: %v", r),
					Type:    "PanicError",
				})),
				Completed: true,
			}
			err = nil
		}
	}()

	out, wfErr := def(env, env.input)
	if wfErr != nil {
		env.newCommands = append(env.newCommands, failWorkflowCommand(&commonv1.Failure{
			Message: wfErr.Error(),
			Type:    "ApplicationError",
		}))
		return DecisionResult{Commands: env.newCommands, Completed: true}, nil
	}
	env.newCommands = append(env.newCommands, completeWorkflowCommand(out))
	return DecisionResult{Commands: env.newCommands, Completed: true}, nil
}

func newEnvironment(taskQueue string, history []*commonv1.HistoryEvent, logger *slog.Logger) *environment {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	env := &environment{taskQueue: taskQueue, replaying: true, logger: logger}

	// Index history: build ordered projections of activities, timers and
	// signals, and resolve their terminal states.
	byScheduledID := map[int64]*activityState{}
	timerByStartedID := map[int64]*timerState{}

	for _, e := range history {
		if e.EventTime != nil {
			if t := e.EventTime.AsTime(); t.After(env.now) {
				env.now = t
			}
		}
		switch a := e.Attributes.(type) {
		case *commonv1.HistoryEvent_WorkflowExecutionStarted:
			env.input = a.WorkflowExecutionStarted.Input
		case *commonv1.HistoryEvent_ActivityTaskScheduled:
			st := &activityState{activityType: a.ActivityTaskScheduled.ActivityType}
			env.activities = append(env.activities, st)
			byScheduledID[e.EventId] = st
		case *commonv1.HistoryEvent_ActivityTaskCompleted:
			if st := byScheduledID[a.ActivityTaskCompleted.ScheduledEventId]; st != nil {
				st.completed = true
				st.result = a.ActivityTaskCompleted.Result
			}
		case *commonv1.HistoryEvent_ActivityTaskFailed:
			if st := byScheduledID[a.ActivityTaskFailed.ScheduledEventId]; st != nil {
				st.failed = true
				st.failure = a.ActivityTaskFailed.Failure
			}
		case *commonv1.HistoryEvent_TimerStarted:
			st := &timerState{}
			env.timers = append(env.timers, st)
			timerByStartedID[e.EventId] = st
		case *commonv1.HistoryEvent_TimerFired:
			if st := timerByStartedID[a.TimerFired.StartedEventId]; st != nil {
				st.fired = true
			}
		case *commonv1.HistoryEvent_WorkflowExecutionSignaled:
			env.signals = append(env.signals, &signalRecord{
				name:  a.WorkflowExecutionSignaled.SignalName,
				input: a.WorkflowExecutionSignaled.Input,
			})
		case *commonv1.HistoryEvent_WorkflowExecutionCompleted,
			*commonv1.HistoryEvent_WorkflowExecutionFailed:
			env.terminated = true
		}
	}
	if env.now.IsZero() {
		env.now = time.Now()
	}
	return env
}

// --- Context implementation ---

func (e *environment) ExecuteActivity(activityType string, input any, opts ActivityOptions) Future {
	idx := e.activitySeq
	e.activitySeq++

	if idx < len(e.activities) {
		st := e.activities[idx]
		if st.activityType != "" && st.activityType != activityType {
			panic(fmt.Errorf("%w: activity #%d recorded as %q but code scheduled %q",
				ErrNonDeterministic, idx, st.activityType, activityType))
		}
		switch {
		case st.completed:
			return &future{ready: true, value: st.result}
		case st.failed:
			return &future{ready: true, err: activityErrorFrom(activityType, st.failure)}
		default:
			return &future{} // scheduled, not yet resolved
		}
	}

	// New activity: emit a command and record a pending placeholder.
	e.replaying = false
	payload, err := converter.Encode(input)
	if err != nil {
		panic(fmt.Errorf("encode activity input: %w", err))
	}
	activityID := opts.ActivityID
	if activityID == "" {
		activityID = fmt.Sprintf("activity-%d", idx)
	}
	taskQueue := opts.TaskQueue
	if taskQueue == "" {
		taskQueue = e.taskQueue
	}
	e.newCommands = append(e.newCommands, &commonv1.Command{
		Attributes: &commonv1.Command_ScheduleActivityTask{
			ScheduleActivityTask: &commonv1.ScheduleActivityTaskCommand{
				ActivityId:          activityID,
				ActivityType:        activityType,
				TaskQueue:           taskQueue,
				Input:               payload,
				RetryPolicy:         opts.RetryPolicy.ToProto(),
				StartToCloseTimeout: durationProto(opts.StartToCloseTimeout),
			},
		},
	})
	e.activities = append(e.activities, &activityState{activityType: activityType})
	return &future{}
}

func (e *environment) NewTimer(d time.Duration) Future {
	idx := e.timerSeq
	e.timerSeq++

	if idx < len(e.timers) {
		if e.timers[idx].fired {
			return &future{ready: true}
		}
		return &future{}
	}

	e.replaying = false
	e.newCommands = append(e.newCommands, &commonv1.Command{
		Attributes: &commonv1.Command_StartTimer{
			StartTimer: &commonv1.StartTimerCommand{
				TimerId:            fmt.Sprintf("timer-%d", idx),
				StartToFireTimeout: durationProto(d),
			},
		},
	})
	e.timers = append(e.timers, &timerState{})
	return &future{}
}

func (e *environment) Sleep(d time.Duration) error {
	return e.NewTimer(d).Get(nil)
}

func (e *environment) Now() time.Time { return e.now }

func (e *environment) ReceiveSignal(name string) ([]byte, bool) {
	for _, s := range e.signals {
		if s.name == name && !s.consumed {
			s.consumed = true
			return s.input, true
		}
	}
	return nil, false
}

func (e *environment) AwaitSignal(name string) []byte {
	if input, ok := e.ReceiveSignal(name); ok {
		return input
	}
	panic(blockedSignal{}) // resumes on a later decision once the signal arrives
}

func (e *environment) Logger() *slog.Logger {
	if e.replaying {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return e.logger
}

// --- Future ---

type future struct {
	ready bool
	value []byte
	err   error
}

func (f *future) IsReady() bool { return f.ready }

func (f *future) Get(out any) error {
	if !f.ready {
		panic(blockedSignal{})
	}
	if f.err != nil {
		return f.err
	}
	return converter.Decode(f.value, out)
}

// --- helpers ---

func completeWorkflowCommand(result []byte) *commonv1.Command {
	return &commonv1.Command{
		Attributes: &commonv1.Command_CompleteWorkflowExecution{
			CompleteWorkflowExecution: &commonv1.CompleteWorkflowExecutionCommand{Result: result},
		},
	}
}

func failWorkflowCommand(f *commonv1.Failure) *commonv1.Command {
	return &commonv1.Command{
		Attributes: &commonv1.Command_FailWorkflowExecution{
			FailWorkflowExecution: &commonv1.FailWorkflowExecutionCommand{Failure: f},
		},
	}
}

func activityErrorFrom(activityType string, f *commonv1.Failure) error {
	msg := "activity failed"
	if f != nil {
		msg = f.Message
	}
	return &ActivityError{ActivityType: activityType, Message: msg}
}

func durationProto(d time.Duration) *durationpb.Duration {
	if d <= 0 {
		return nil
	}
	return durationpb.New(d)
}
