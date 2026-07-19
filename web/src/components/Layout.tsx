import { Activity, LayoutList, Timer } from "lucide-react";
import type { ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";

export function Layout({ children }: { children: ReactNode }) {
  const loc = useLocation();
  const onList = loc.pathname === "/" || loc.pathname.startsWith("/workflows");
  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">
            <Timer size={17} />
          </div>
          <div>
            <div className="brand-name">Chronos</div>
            <div className="brand-sub">Durable Execution</div>
          </div>
        </div>

        <nav className="nav">
          <div className="nav-section">Observe</div>
          <Link className={`nav-item ${onList ? "active" : ""}`} to="/">
            <LayoutList size={16} />
            Workflows
          </Link>
          <a className="nav-item" href="/metrics" target="_blank" rel="noreferrer">
            <Activity size={16} />
            Metrics
          </a>
        </nav>

        <div className="sidebar-footer">
          <div>
            <span className="dot" />
            Control plane connected
          </div>
          <div>Engine v1.0 · gRPC :7233</div>
        </div>
      </aside>

      <main className="main">{children}</main>
    </div>
  );
}
