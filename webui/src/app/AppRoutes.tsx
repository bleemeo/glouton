import { Navigate, Route, Routes } from "react-router-dom";

import { Containers } from "../containers/Containers";
import { Dashboard } from "../dashboard/Dashboard";
import { Informations } from "../informations/Informations";
import { Processes } from "../processes/Processes";

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/dashboard" replace />} />
      <Route path="/dashboard" element={<Dashboard />} />
      <Route path="/containers" element={<Containers />} />
      <Route path="/processes" element={<Processes />} />
      <Route path="/informations" element={<Informations />} />
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}
