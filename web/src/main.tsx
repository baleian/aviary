import "@fontsource-variable/jetbrains-mono";
import "@fontsource-variable/plus-jakarta-sans";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App.tsx";
import sprite from "./assets/sprite.svg?raw";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/shell.css";
import "./styles/screens.css";

// The symbols that the icons and the logo refer to.
document.body.insertAdjacentHTML("afterbegin", sprite);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
