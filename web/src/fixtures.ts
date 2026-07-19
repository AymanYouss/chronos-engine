import type { EventItem, Execution, Stats } from "./types";

// Fixtures mirror exactly what the Chronos control plane emits (same DTO shapes
// as internal/httpapi). They back the inspector when the API is unreachable so
// the UI is always demonstrable, and they encode the canonical crash-and-resume
// scenario: worker-1 charges payment and reserves inventory, is killed while the
// workflow is parked on its packaging timer, and worker-2 replays history and
// finishes shipment + receipt — with every activity recorded exactly once.

const STAR = "order-demo-4021";
const t = (s: number) => new Date(Date.UTC(2026, 6, 19, 9, 14, s)).toISOString();

export const fixtureHistory: Record<string, EventItem[]> = {
  [STAR]: [
    { eventId: 1, type: "WorkflowExecutionStarted", time: t(2), details: { workflowType: "OrderFulfillment", taskQueue: "default", input: '{"orderId":"order-demo-4021","customerId":"cust-42","amountCents":4999,"items":["widget","gadget"]}' } },
    { eventId: 2, type: "ActivityTaskScheduled", time: t(2), details: { activityId: "activity-0", activityType: "ChargePayment" } },
    { eventId: 3, type: "ActivityTaskStarted", time: t(2), details: { scheduledEventId: 2, worker: "worker-1", attempt: 1 } },
    { eventId: 4, type: "ActivityTaskCompleted", time: t(3), details: { scheduledEventId: 2, result: '"ch_order-demo-4021"' } },
    { eventId: 5, type: "ActivityTaskScheduled", time: t(3), details: { activityId: "activity-1", activityType: "ReserveInventory" } },
    { eventId: 6, type: "ActivityTaskStarted", time: t(3), details: { scheduledEventId: 5, worker: "worker-1", attempt: 1 } },
    { eventId: 7, type: "ActivityTaskCompleted", time: t(4), details: { scheduledEventId: 5, result: '"rsv_order-demo-4021"' } },
    { eventId: 8, type: "TimerStarted", time: t(4), details: { timerId: "timer-0", fireTime: t(9) } },
    { eventId: 9, type: "TimerFired", time: t(9), details: { timerId: "timer-0", startedEventId: 8 } },
    { eventId: 10, type: "ActivityTaskScheduled", time: t(9), details: { activityId: "activity-2", activityType: "ShipOrder" } },
    { eventId: 11, type: "ActivityTaskStarted", time: t(9), details: { scheduledEventId: 10, worker: "worker-2", attempt: 1 } },
    { eventId: 12, type: "ActivityTaskCompleted", time: t(10), details: { scheduledEventId: 10, result: '"1Zorder-demo-4021"' } },
    { eventId: 13, type: "ActivityTaskScheduled", time: t(10), details: { activityId: "activity-3", activityType: "SendReceipt" } },
    { eventId: 14, type: "ActivityTaskStarted", time: t(10), details: { scheduledEventId: 13, worker: "worker-2", attempt: 1 } },
    { eventId: 15, type: "ActivityTaskCompleted", time: t(11), details: { scheduledEventId: 13, result: '"rcpt_order-demo-4021"' } },
    { eventId: 16, type: "WorkflowExecutionCompleted", time: t(11), details: { result: '{"orderId":"order-demo-4021","status":"FULFILLED","trackingNo":"1Zorder-demo-4021"}' } },
  ],
  "order-8837": [
    { eventId: 1, type: "WorkflowExecutionStarted", time: t(20), details: { workflowType: "OrderFulfillment", taskQueue: "default" } },
    { eventId: 2, type: "ActivityTaskScheduled", time: t(20), details: { activityId: "activity-0", activityType: "ChargePayment" } },
    { eventId: 3, type: "ActivityTaskStarted", time: t(20), details: { scheduledEventId: 2, worker: "worker-3", attempt: 1 } },
    { eventId: 4, type: "ActivityTaskCompleted", time: t(21), details: { scheduledEventId: 2, result: '"ch_order-8837"' } },
    { eventId: 5, type: "ActivityTaskScheduled", time: t(21), details: { activityId: "activity-1", activityType: "ReserveInventory" } },
    { eventId: 6, type: "ActivityTaskStarted", time: t(21), details: { scheduledEventId: 5, worker: "worker-3", attempt: 1 } },
  ],
  "order-8830": [
    { eventId: 1, type: "WorkflowExecutionStarted", time: t(0), details: { workflowType: "OrderFulfillment", taskQueue: "default" } },
    { eventId: 2, type: "ActivityTaskScheduled", time: t(0), details: { activityId: "activity-0", activityType: "ChargePayment" } },
    { eventId: 3, type: "ActivityTaskStarted", time: t(0), details: { scheduledEventId: 2, worker: "worker-2", attempt: 1 } },
    { eventId: 4, type: "ActivityTaskRetryScheduled", time: t(1), details: { scheduledEventId: 2, attempt: 2, error: "payment gateway timeout", nextAttempt: t(3) } },
    { eventId: 5, type: "ActivityTaskStarted", time: t(3), details: { scheduledEventId: 2, worker: "worker-2", attempt: 2 } },
    { eventId: 6, type: "ActivityTaskFailed", time: t(4), details: { scheduledEventId: 2, retryable: false, error: "card declined: insufficient funds" } },
    { eventId: 7, type: "WorkflowExecutionFailed", time: t(4), details: { error: "ChargePayment: card declined: insufficient funds" } },
  ],
};

export const fixtureWorkflows: Execution[] = [
  { workflowId: STAR, runId: "b3f1c2a4-8e7d-4c2b-9f1a-2d3e4f5a6b7c", workflowType: "OrderFulfillment", taskQueue: "default", status: "completed", startTime: t(2), closeTime: t(11), historyLength: 16 },
  { workflowId: "order-8837", runId: "c4a2d3b5-9f8e-4d3c-8a2b-3e4f5a6b7c8d", workflowType: "OrderFulfillment", taskQueue: "default", status: "running", startTime: t(20), historyLength: 6 },
  { workflowId: "order-8836", runId: "d5b3e4c6-a09f-4e4d-9b3c-4f5a6b7c8d9e", workflowType: "OrderFulfillment", taskQueue: "default", status: "completed", startTime: t(0), closeTime: t(12), historyLength: 16 },
  { workflowId: "order-8835", runId: "e6c4f5d7-b1a0-4f5e-ac4d-5a6b7c8d9e0f", workflowType: "OrderFulfillment", taskQueue: "default", status: "running", startTime: t(0), historyLength: 9 },
  { workflowId: "order-8830", runId: "f7d5a6e8-c2b1-4a6f-bd5e-6b7c8d9e0f1a", workflowType: "OrderFulfillment", taskQueue: "default", status: "failed", startTime: t(0), closeTime: t(4), historyLength: 7 },
  { workflowId: "order-8829", runId: "a8e6b7f9-d3c2-4b70-ce6f-7c8d9e0f1a2b", workflowType: "OrderFulfillment", taskQueue: "default", status: "completed", startTime: t(0), closeTime: t(13), historyLength: 16 },
];

export const fixtureStats: Stats = {
  running: 2,
  completed: 3,
  failed: 1,
  timedOut: 0,
  terminated: 0,
  total: 6,
};

export function fixtureHistoryFor(workflowId: string): EventItem[] {
  return fixtureHistory[workflowId] ?? fixtureHistory[STAR];
}

export const STAR_WORKFLOW_ID = STAR;
