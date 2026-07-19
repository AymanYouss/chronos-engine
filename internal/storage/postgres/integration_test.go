package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/storage"
	"github.com/AymanYouss/chronos-engine/internal/storage/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
)

// newStore connects to the Postgres instance named by CHRONOS_TEST_DSN and
// applies migrations. Tests are skipped when the DSN is not set so unit runs
// stay hermetic; CI sets it against a Postgres service container.
func newStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := os.Getenv("CHRONOS_TEST_DSN")
	if dsn == "" {
		t.Skip("CHRONOS_TEST_DSN not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	store, err := postgres.New(ctx, dsn)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(ctx))
	t.Cleanup(store.Close)
	return store
}

func scheduleActivityCmd(id, actType, queue string) *commonv1.Command {
	return &commonv1.Command{Attributes: &commonv1.Command_ScheduleActivityTask{
		ScheduleActivityTask: &commonv1.ScheduleActivityTaskCommand{
			ActivityId: id, ActivityType: actType, TaskQueue: queue,
		},
	}}
}

func completeWorkflowCmd() *commonv1.Command {
	return &commonv1.Command{Attributes: &commonv1.Command_CompleteWorkflowExecution{
		CompleteWorkflowExecution: &commonv1.CompleteWorkflowExecutionCommand{Result: []byte(`"done"`)},
	}}
}

func countEvents(events []*commonv1.HistoryEvent, t commonv1.EventType) int {
	n := 0
	for _, e := range events {
		if e.EventType == t {
			n++
		}
	}
	return n
}

func TestStartWorkflowIsIdempotent(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	wfID := "wf-idem-" + uuid.NewString()

	p := storage.StartWorkflowParams{WorkflowID: wfID, WorkflowType: "T", TaskQueue: "q"}
	run1, already1, err := store.StartWorkflow(ctx, p)
	require.NoError(t, err)
	require.False(t, already1)

	run2, already2, err := store.StartWorkflow(ctx, p)
	require.NoError(t, err)
	require.True(t, already2, "second start must be idempotent")
	require.Equal(t, run1, run2, "idempotent start returns the same run")
}

func TestExactlyOnceActivityRecording(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	queue := "q-" + uuid.NewString()
	wfID := "wf-once-" + uuid.NewString()

	_, _, err := store.StartWorkflow(ctx, storage.StartWorkflowParams{WorkflowID: wfID, WorkflowType: "T", TaskQueue: queue})
	require.NoError(t, err)

	// First workflow task: schedule an activity.
	wt, err := store.PollWorkflowTask(ctx, queue, "worker-1", 30*time.Second)
	require.NoError(t, err)
	require.NotNil(t, wt)
	require.NoError(t, store.CompleteWorkflowTask(ctx, wt.TaskToken, "worker-1",
		[]*commonv1.Command{scheduleActivityCmd("act-0", "DoThing", queue)}))

	// Poll the activity, then complete it twice: the duplicate must be rejected
	// so exactly one ACTIVITY_TASK_COMPLETED is recorded.
	at, err := store.PollActivityTask(ctx, queue, "worker-1", 30*time.Second)
	require.NoError(t, err)
	require.NotNil(t, at)

	require.NoError(t, store.CompleteActivityTask(ctx, at.TaskToken, "worker-1", []byte(`"result"`)))
	dupErr := store.CompleteActivityTask(ctx, at.TaskToken, "worker-1", []byte(`"result"`))
	require.ErrorIs(t, dupErr, storage.ErrTaskLost, "duplicate completion must be rejected")

	history, err := store.GetHistory(ctx, wfID, at.RunID)
	require.NoError(t, err)
	require.Equal(t, 1, countEvents(history, commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED),
		"activity result must be recorded exactly once")
}

func TestResumePreservesCompletedActivity(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	queue := "q-" + uuid.NewString()
	wfID := "wf-resume-" + uuid.NewString()

	_, _, err := store.StartWorkflow(ctx, storage.StartWorkflowParams{WorkflowID: wfID, WorkflowType: "T", TaskQueue: queue})
	require.NoError(t, err)

	wt, err := store.PollWorkflowTask(ctx, queue, "worker-1", 30*time.Second)
	require.NoError(t, err)
	require.NoError(t, store.CompleteWorkflowTask(ctx, wt.TaskToken, "worker-1",
		[]*commonv1.Command{scheduleActivityCmd("act-0", "DoThing", queue)}))

	at, err := store.PollActivityTask(ctx, queue, "worker-1", 30*time.Second)
	require.NoError(t, err)
	require.NoError(t, store.CompleteActivityTask(ctx, at.TaskToken, "worker-1", []byte(`"result"`)))

	// Simulate a crashed worker: a new worker polls the pending workflow task.
	// The history it receives must contain the completed activity so replay
	// does not re-run it.
	wt2, err := store.PollWorkflowTask(ctx, queue, "worker-2", 30*time.Second)
	require.NoError(t, err)
	require.NotNil(t, wt2, "a workflow task should be pending after activity completion")
	require.Equal(t, 1, countEvents(wt2.History, commonv1.EventType_EVENT_TYPE_ACTIVITY_TASK_COMPLETED))

	require.NoError(t, store.CompleteWorkflowTask(ctx, wt2.TaskToken, "worker-2",
		[]*commonv1.Command{completeWorkflowCmd()}))

	exec, err := store.DescribeWorkflow(ctx, wfID, wt2.RunID)
	require.NoError(t, err)
	require.Equal(t, commonv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED, exec.Status)
}

func TestActivityRetryWithBackoff(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	queue := "q-" + uuid.NewString()
	wfID := "wf-retry-" + uuid.NewString()

	_, _, err := store.StartWorkflow(ctx, storage.StartWorkflowParams{WorkflowID: wfID, WorkflowType: "T", TaskQueue: queue})
	require.NoError(t, err)
	wt, err := store.PollWorkflowTask(ctx, queue, "worker-1", 30*time.Second)
	require.NoError(t, err)

	cmd := &commonv1.Command{Attributes: &commonv1.Command_ScheduleActivityTask{
		ScheduleActivityTask: &commonv1.ScheduleActivityTaskCommand{
			ActivityId: "act-0", ActivityType: "Flaky", TaskQueue: queue,
			RetryPolicy: &commonv1.RetryPolicy{
				InitialInterval: durationpb.New(time.Millisecond), BackoffCoefficient: 2, MaxAttempts: 3,
			},
		},
	}}
	require.NoError(t, store.CompleteWorkflowTask(ctx, wt.TaskToken, "worker-1", []*commonv1.Command{cmd}))

	at, err := store.PollActivityTask(ctx, queue, "worker-1", 30*time.Second)
	require.NoError(t, err)
	require.Equal(t, int32(1), at.Attempt)

	retried, err := store.FailActivityTask(ctx, at.TaskToken, "worker-1",
		&commonv1.Failure{Message: "boom"}, true)
	require.NoError(t, err)
	require.True(t, retried, "first failure under a 3-attempt policy should retry")

	// After the backoff elapses the task is redelivered with an incremented attempt.
	require.Eventually(t, func() bool {
		at2, err := store.PollActivityTask(ctx, queue, "worker-1", 30*time.Second)
		return err == nil && at2 != nil && at2.Attempt == 2
	}, 3*time.Second, 50*time.Millisecond, "activity should be redelivered as attempt 2")
}

func TestDurableTimerFires(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	queue := "q-" + uuid.NewString()
	wfID := "wf-timer-" + uuid.NewString()

	_, _, err := store.StartWorkflow(ctx, storage.StartWorkflowParams{WorkflowID: wfID, WorkflowType: "T", TaskQueue: queue})
	require.NoError(t, err)
	wt, err := store.PollWorkflowTask(ctx, queue, "worker-1", 30*time.Second)
	require.NoError(t, err)

	timerCmd := &commonv1.Command{Attributes: &commonv1.Command_StartTimer{
		StartTimer: &commonv1.StartTimerCommand{TimerId: "t-0", StartToFireTimeout: durationpb.New(10 * time.Millisecond)},
	}}
	require.NoError(t, store.CompleteWorkflowTask(ctx, wt.TaskToken, "worker-1", []*commonv1.Command{timerCmd}))

	time.Sleep(50 * time.Millisecond)
	fired, err := store.FireDueTimers(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, fired, 1)

	history, err := store.GetHistory(ctx, wfID, wt.RunID)
	require.NoError(t, err)
	require.Equal(t, 1, countEvents(history, commonv1.EventType_EVENT_TYPE_TIMER_FIRED))
}
