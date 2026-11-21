import { useEffect } from "react";

// useTitle names the tab after the screen: "Projects · Aviary".
export function useTitle(...parts: (string | undefined)[]) {
  const title = [...parts, "Aviary"].filter(Boolean).join(" · ");
  useEffect(() => {
    document.title = title;
  }, [title]);
}
