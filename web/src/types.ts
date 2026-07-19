export type WorkflowStatus =
  | "running"
  | "completed"
  | "failed"
  | "timedOut"
  | "terminated"
  | "unspecified";

export interface Execution {
  workflowId: string;
  runId: string;
  workflowType: string;
  taskQueue: string;
  status: WorkflowStatus;
  startTime: string;
  closeTime?: string;
  historyLength: number;
}

export interface EventItem {
  eventId: number;
  type: string;
  time: string;
  details?: Record<string, unknown>;
}

export interface Stats {
  running: number;
  completed: number;
  failed: number;
  timedOut: number;
  terminated: number;
  total: number;
}
