import { Navigate, Route, Routes } from "react-router";
import { Account } from "./screens/Account.tsx";
import { Projects } from "./screens/Projects.tsx";
import { SignIn } from "./screens/SignIn.tsx";
import { NotFound, Signed } from "./shell/Signed.tsx";
import { Toasts } from "./ui/Toasts.tsx";

// App is the console: its screens, by their paths, and the messages that
// show above them.
export function App() {
  return (
    <>
      <Routes>
        <Route path="/signin" element={<SignIn />} />
        <Route element={<Signed />}>
          <Route index element={<Navigate to="/projects" replace />} />
          <Route path="/projects" element={<Projects />} />
          <Route path="/me" element={<Account />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
      <Toasts />
    </>
  );
}
