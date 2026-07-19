import type { WorkflowStatus } from "../types";

const LABEL: Record<WorkflowStatus, string> = {
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  timedOut: "Timed out",
  terminated: "Terminated",
  unspecified: "Unknown",
};

export function StatusPill({ status }: { status: WorkflowStatus }) {
  const cls =
    status === "running" || status === "completed" || status === "failed"
      ? status
      : "failed";
  return (
    <span className={`pill ${cls}`}>
      <span className="glyph" />
      {LABEL[status] ?? status}
    </span>
  );
}
