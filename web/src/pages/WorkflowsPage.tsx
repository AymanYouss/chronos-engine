import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, CircleSlash, Layers, Loader2, Search, XCircle } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { getStats, listWorkflows } from "../api";
import { Layout } from "../components/Layout";
import { StatusPill } from "../components/StatusPill";
import { relativeTime, shortRun } from "../lib/format";

const FILTERS = [
  { key: "all", label: "All" },
  { key: "running", label: "Running" },
  { key: "completed", label: "Completed" },
  { key: "failed", label: "Failed" },
];

export function WorkflowsPage() {
  const [filter, setFilter] = useState("all");
  const [query, setQuery] = useState("");
  const navigate = useNavigate();

  const stats = useQuery({ queryKey: ["stats"], queryFn: getStats, refetchInterval: 4000 });
  const workflows = useQuery({
    queryKey: ["workflows", filter],
    queryFn: () => listWorkflows(filter),
    refetchInterval: 4000,
  });

  const rows = (workflows.data ?? []).filter((w) =>
    query ? w.workflowId.toLowerCase().includes(query.toLowerCase()) : true,
  );

  return (
    <Layout>
      <div className="topbar">
        <div>
          <div className="crumb">Chronos / Workflows</div>
          <h1>Workflows</h1>
        </div>
      </div>

      <div className="content">
        <div className="stat-grid">
          <StatCard icon={<Layers size={14} />} label="Total executions" value={stats.data?.total ?? 0} />
          <StatCard icon={<Loader2 size={14} />} label="Running" value={stats.data?.running ?? 0} accent="running" />
          <StatCard icon={<CheckCircle2 size={14} />} label="Completed" value={stats.data?.completed ?? 0} accent="completed" />
          <StatCard icon={<XCircle size={14} />} label="Failed" value={stats.data?.failed ?? 0} accent="failed" />
        </div>

        <div className="toolbar">
          <label className="search">
            <Search size={15} color="var(--text-dim)" />
            <input
              placeholder="Search by workflow ID…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </label>
          <div className="segmented">
            {FILTERS.map((f) => (
              <button
                key={f.key}
                className={filter === f.key ? "active" : ""}
                onClick={() => setFilter(f.key)}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>

        <div className="card">
          <table className="wf">
            <thead>
              <tr>
                <th>Workflow ID</th>
                <th>Type</th>
                <th>Status</th>
                <th>Run</th>
                <th>Events</th>
                <th>Started</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((w) => (
                <tr
                  key={`${w.workflowId}/${w.runId}`}
                  onClick={() => navigate(`/workflows/${encodeURIComponent(w.workflowId)}/${w.runId}`)}
                >
                  <td className="wf-id">{w.workflowId}</td>
                  <td className="wf-type">{w.workflowType}</td>
                  <td>
                    <StatusPill status={w.status} />
                  </td>
                  <td className="mono dim">{shortRun(w.runId)}</td>
                  <td className="mono">{w.historyLength}</td>
                  <td className="muted">{relativeTime(w.startTime)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length === 0 && (
            <div className="empty">
              <CircleSlash size={20} style={{ marginBottom: 8 }} />
              <div>No workflows match this view.</div>
            </div>
          )}
        </div>
      </div>
    </Layout>
  );
}

function StatCard({
  icon,
  label,
  value,
  accent,
}: {
  icon: React.ReactNode;
  label: string;
  value: number;
  accent?: string;
}) {
  return (
    <div className="stat-card">
      <div className="label">
        {icon}
        {label}
      </div>
      <div className={`value ${accent ? `accent-${accent}` : ""}`}>{value}</div>
    </div>
  );
}
