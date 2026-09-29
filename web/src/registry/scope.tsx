import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { WidgetPreferences } from "@docket/plugin-ui";
import type { StreamConfig } from "../types";
import { ReferenceRegistry, useRegistryVersion } from "./registry";
import type { WidgetRouter } from "./widget-state";
const defaults: WidgetPreferences = {
  theme: "light",
  density: "comfortable",
  reducedMotion: false,
};
export type PluginEnvironment = {
  workspace: string;
  config: StreamConfig;
  preferences: WidgetPreferences;
  router?: WidgetRouter;
  references?: ReferenceRegistry;
  connection: string;
  refreshTask?(taskId: string): void;
};
const Scope = createContext<PluginEnvironment>({
  workspace: "",
  config: { statuses: [], terminal: [], labels: [], plugins: [] },
  preferences: defaults,
  connection: "idle",
});
export const usePluginScope = () => useContext(Scope);
export function PluginScope({
  workspace,
  config,
  router,
  connection,
  theme = "system",
  density = "comfortable",
  refreshTask,
  children,
}: {
  workspace: string;
  config: StreamConfig;
  router?: WidgetRouter;
  connection: string;
  theme?: "light" | "dark" | "system";
  density?: WidgetPreferences["density"];
  refreshTask?(taskId: string): void;
  children: ReactNode;
}) {
  const version = useRegistryVersion();
  const [media, setMedia] = useState(() => ({
    dark:
      typeof matchMedia === "function" &&
      matchMedia("(prefers-color-scheme: dark)").matches,
    motion:
      typeof matchMedia === "function" &&
      matchMedia("(prefers-reduced-motion: reduce)").matches,
  }));
  useEffect(() => {
    if (typeof matchMedia !== "function") return;
    const dark = matchMedia("(prefers-color-scheme: dark)"),
      motion = matchMedia("(prefers-reduced-motion: reduce)");
    const update = () =>
      setMedia({ dark: dark.matches, motion: motion.matches });
    dark.addEventListener("change", update);
    motion.addEventListener("change", update);
    return () => {
      dark.removeEventListener("change", update);
      motion.removeEventListener("change", update);
    };
  }, []);
  const key = JSON.stringify([config.plugins, config.resolver_generation]);
  const references = useMemo(
    () =>
      new ReferenceRegistry(
        workspace,
        config.resolver_generation || "",
        config.plugins || [],
      ),
    [workspace, key, version],
  );
  useEffect(() => () => references.destroy(), [references]);
  const preferences: WidgetPreferences = {
    theme: theme === "system" ? (media.dark ? "dark" : "light") : theme,
    density,
    reducedMotion: media.motion,
  };
  return (
    <Scope.Provider
      value={{
        workspace,
        config,
        preferences,
        router,
        connection,
        references,
        refreshTask,
      }}
    >
      {children}
    </Scope.Provider>
  );
}
