import { useEffect, useState } from "react";

/** Tiny hash router: works when the panel is served from any path or as one file. */
export function useRoute(): string {
  const get = () => window.location.hash.replace(/^#/, "") || "/";
  const [route, setRoute] = useState(get);
  useEffect(() => {
    const on = () => {
      setRoute(get());
      window.scrollTo({ top: 0 });
    };
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function navigate(to: string) {
  window.location.hash = to;
}

export const href = (to: string) => `#${to}`;
