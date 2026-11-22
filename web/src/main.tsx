import "@fontsource-variable/jetbrains-mono";
import "@fontsource-variable/plus-jakarta-sans";
import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./App.tsx";
import sprite from "./assets/sprite.svg?raw";
import { queryClient } from "./query.ts";
import { start } from "./session/auth.ts";
import { Wait } from "./shell/Signed.tsx";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/shell.css";
import "./styles/screens.css";

// The symbols that the icons and the logo refer to.
document.body.insertAdjacentHTML("afterbegin", sprite);

const root = createRoot(document.getElementById("root")!);
root.render(<Wait />);
// The console shows once it knows who is signed in, so that its first
// requests carry the caller's token.
void start().finally(() =>
  root.render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </StrictMode>,
  ),
);
