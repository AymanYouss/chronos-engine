import {
  AlarmClockCheck,
  CircleCheckBig,
  CircleCheck,
  CirclePlus,
  Hourglass,
  OctagonX,
  Play,
  Radio,
  RotateCcw,
  Rocket,
  TriangleAlert,
  Workflow,
} from "lucide-react";
import type { JSX } from "react";
import type { EventItem } from "../types";
import { clockTime } from "../lib/format";

type Category = "lifecycle" | "activity" | "failure" | "timer" | "signal";

const META: Record<string, { icon: JSX.Element; cat: Category }> = {
  WorkflowExecutionStarted: { icon: <Play size={15} />, cat: "lifecycle" },
  ActivityTaskScheduled: { icon: <CirclePlus size={15} />, cat: "activity" },
  ActivityTaskStarted: { icon: <Rocket size={15} />, cat: "activity" },
  ActivityTaskCompleted: { icon: <CircleCheck size={15} />, cat: "activity" },
  ActivityTaskFailed: { icon: <TriangleAlert size={15} />, cat: "failure" },
  ActivityTaskRetryScheduled: { icon: <RotateCcw size={15} />, cat: "failure" },
  TimerStarted: { icon: <Hourglass size={15} />, cat: "timer" },
  TimerFired: { icon: <AlarmClockCheck size={15} />, cat: "timer" },
  WorkflowExecutionSignaled: { icon: <Radio size={15} />, cat: "signal" },
  WorkflowExecutionCompleted: { icon: <CircleCheckBig size={15} />, cat: "activity" },
  WorkflowExecutionFailed: { icon: <OctagonX size={15} />, cat: "failure" },
};

function humanize(type: string): string {
  return type.replace(/([a-z])([A-Z])/g, "$1 $2");
}

const HIDE_KEYS = new Set(["worker", "scheduledEventId", "startedEventId"]);

function renderDetails(details: Record<string, unknown>) {
  const entries = Object.entries(details).filter(
    ([k, v]) => !HIDE_KEYS.has(k) && v !== "" && v !== undefined && v !== null,
  );
  return entries.map(([k, v]) => (
    <span className="kv" key={k}>
      <span className="kk">{k}</span>
      <span className="vv">{String(v)}</span>
    </span>
  ));
}

export function EventTimeline({ events }: { events: EventItem[] }) {
  let lastWorker: string | null = null;
  const rows: JSX.Element[] = [];

  events.forEach((e) => {
    const worker = typeof e.details?.worker === "string" ? (e.details.worker as string) : null;
    if (worker && lastWorker && worker !== lastWorker) {
      rows.push(
        <div className="resume-marker" key={`resume-${e.eventId}`}>
          <div className="event-node cat-lifecycle">
            <Workflow size={15} />
          </div>
          <div className="resume-line">
            <span>
              Execution resumed on <span className="badge">{worker}</span> — history
              replayed deterministically, no completed activity re-run.
            </span>
          </div>
        </div>,
      );
    }
    if (worker) lastWorker = worker;

    const meta = META[e.type] ?? { icon: <CirclePlus size={15} />, cat: "lifecycle" as Category };
    rows.push(
      <div className="event" key={e.eventId}>
        <div className={`event-node cat-${meta.cat}`}>{meta.icon}</div>
        <div className="event-body">
          <div className="event-row1">
            <span className="event-name">{humanize(e.type)}</span>
            <span className="event-id">#{e.eventId}</span>
            {worker && (
              <span className="worker-tag">
                <Rocket size={11} /> {worker}
              </span>
            )}
            <span className="event-time">{clockTime(e.time)}</span>
          </div>
          {e.details && Object.keys(e.details).length > 0 && (
            <div className="event-detail">{renderDetails(e.details)}</div>
          )}
        </div>
      </div>,
    );
  });

  return <div className="timeline">{rows}</div>;
}

export function hasWorkerHandoff(events: EventItem[]): string | null {
  let last: string | null = null;
  for (const e of events) {
    const w = typeof e.details?.worker === "string" ? (e.details.worker as string) : null;
    if (w && last && w !== last) return w;
    if (w) last = w;
  }
  return null;
}
