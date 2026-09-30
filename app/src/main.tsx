import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/700.css";
import App from "./App";
import { bootstrapTokenFromHash, watchTokenHash } from "./api/api";

// Before anything reads location.hash (the app reads channel links from it).
bootstrapTokenFromHash();
watchTokenHash();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
