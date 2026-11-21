import "@fontsource-variable/jetbrains-mono";
import "@fontsource-variable/plus-jakarta-sans";
import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./App.tsx";
import sprite from "./assets/sprite.svg?raw";
import { queryClient } from "./query.ts";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/shell.css";
import "./styles/screens.css";

// The symbols that the icons and the logo refer to.
document.body.insertAdjacentHTML("afterbegin", sprite);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
