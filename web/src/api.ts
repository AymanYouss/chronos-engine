import {
  fixtureHistoryFor,
  fixtureStats,
  fixtureWorkflows,
} from "./fixtures";
import type { EventItem, Execution, Stats } from "./types";

// The inspector reads live data from the control-plane REST API. If the API is
// unreachable (e.g. viewing the UI without a running backend) it transparently
// falls back to representative fixtures so the interface is always populated.
const API = "";

async function get<T>(path: string, fallback: T): Promise<T> {
  try {
    const res = await fetch(`${API}${path}`, { headers: { Accept: "application/json" } });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return (await res.json()) as T;
  } catch {
    return fallback;
  }
}

export async function getStats(): Promise<Stats> {
  return get<Stats>("/api/stats", fixtureStats);
}

export async function listWorkflows(status?: string): Promise<Execution[]> {
  const qs = status && status !== "all" ? `?status=${encodeURIComponent(status)}` : "";
  const fallback = status && status !== "all"
    ? fixtureWorkflows.filter((w) => w.status === status)
    : fixtureWorkflows;
  const data = await get<{ workflows: Execution[] }>(`/api/workflows${qs}`, { workflows: fallback });
  return data.workflows ?? fallback;
}

export async function getWorkflow(workflowId: string, runId: string): Promise<Execution> {
  const fallback =
    fixtureWorkflows.find((w) => w.workflowId === workflowId) ?? fixtureWorkflows[0];
  return get<Execution>(`/api/workflows/${encodeURIComponent(workflowId)}/${encodeURIComponent(runId)}`, fallback);
}

export async function getHistory(workflowId: string, runId: string): Promise<EventItem[]> {
  const fallback = fixtureHistoryFor(workflowId);
  const data = await get<{ events: EventItem[] }>(
    `/api/workflows/${encodeURIComponent(workflowId)}/${encodeURIComponent(runId)}/history`,
    { events: fallback },
  );
  return data.events ?? fallback;
}

export async function startWorkflow(body: {
  workflowId: string;
  workflowType: string;
  taskQueue: string;
  input: string;
}): Promise<void> {
  await fetch(`${API}/api/workflows`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}
