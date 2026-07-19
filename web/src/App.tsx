import { Route, Routes } from "react-router-dom";
import { WorkflowDetailPage } from "./pages/WorkflowDetailPage";
import { WorkflowsPage } from "./pages/WorkflowsPage";

export function App() {
  return (
    <Routes>
      <Route path="/" element={<WorkflowsPage />} />
      <Route path="/workflows/:workflowId/:runId" element={<WorkflowDetailPage />} />
    </Routes>
  );
}
