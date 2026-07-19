import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, ListTree, ShieldCheck } from "lucide-react";
import { Link, useParams } from "react-router-dom";
import { getHistory, getWorkflow } from "../api";
import { EventTimeline, hasWorkerHandoff } from "../components/EventTimeline";
import { Layout } from "../components/Layout";
import { StatusPill } from "../components/StatusPill";
import { clockTime, duration, relativeTime } from "../lib/format";

export function WorkflowDetailPage() {
  const { workflowId = "", runId = "" } = useParams();

  const wf = useQuery({
    queryKey: ["workflow", workflowId, runId],
    queryFn: () => getWorkflow(workflowId, runId),
    refetchInterval: 3000,
  });
  const history = useQuery({
    queryKey: ["history", workflowId, runId],
    queryFn: () => getHistory(workflowId, runId),
    refetchInterval: 3000,
  });

  const events = history.data ?? [];
  const handoffWorker = hasWorkerHandoff(events);

  return (
    <Layout>
      <div className="topbar">
        <div>
          <div className="crumb">
            <Link to="/" style={{ display: "inline-flex", alignItems: "center", gap: 5 }}>
              <ArrowLeft size={13} /> Workflows
            </Link>
            / {workflowId}
          </div>
          <h1>Execution detail</h1>
        </div>
        {wf.data && <StatusPill status={wf.data.status} />}
      </div>

      <div className="content">
        <div className="detail-head">
          <div className="detail-title">
            <ListTree size={20} color="var(--accent)" />
            {workflowId}
          </div>
        </div>

        {handoffWorker && (
          <div className="callout">
            <ShieldCheck size={20} className="ico" />
            <div>
              <b>Resumed after a worker crash.</b> A worker was lost mid-execution; the
              workflow was replayed from its event history on <b>{handoffWorker}</b> and
              completed with <b>zero duplicated side effects</b>.
            </div>
          </div>
        )}

        {wf.data && (
          <div className="meta-grid">
            <Meta k="Run ID" v={wf.data.runId} />
            <Meta k="Workflow type" v={wf.data.workflowType} />
            <Meta k="Task queue" v={wf.data.taskQueue} />
            <Meta k="History length" v={String(wf.data.historyLength)} />
            <Meta k="Started" v={`${clockTime(wf.data.startTime)} · ${relativeTime(wf.data.startTime)}`} />
            <Meta k="Closed" v={wf.data.closeTime ? clockTime(wf.data.closeTime) : "—"} />
            <Meta k="Duration" v={duration(wf.data.startTime, wf.data.closeTime)} />
            <Meta k="Status" v={wf.data.status} />
          </div>
        )}

        <div className="section-title">
          Event history
          <span className="count">{events.length} events</span>
        </div>

        {history.isLoading ? (
          <div className="loading">
            <div className="spinner" />
            Loading history…
          </div>
        ) : (
          <EventTimeline events={events} />
        )}
      </div>
    </Layout>
  );
}

function Meta({ k, v }: { k: string; v: string }) {
  return (
    <div className="meta-cell">
      <div className="k">{k}</div>
      <div className="v">{v}</div>
    </div>
  );
}
