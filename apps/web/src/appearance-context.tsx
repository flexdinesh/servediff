import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { readDiffTheme, readLineDiffType } from "./display-options.ts";
import { save, saved } from "./preferences.ts";
import { useSidebarState } from "./sidebar-context.tsx";
import {
  readThemePreference,
  resolveTheme,
  THEME_STORAGE_KEY,
} from "./theme.ts";

function useAppearanceState(mobile: boolean) {
  const [layout, setDesktopLayout] = useState<"split" | "unified">(() =>
    saved("layout") === "unified" ? "unified" : "split",
  );
  const [narrowLayout, setNarrowLayout] = useState<"split" | "unified" | null>(
    null,
  );
  const [themePreference, setThemePreference] = useState(() =>
    readThemePreference(saved(THEME_STORAGE_KEY)),
  );
  const [systemDark, setSystemDark] = useState(
    () => window.matchMedia("(prefers-color-scheme: dark)").matches,
  );
  const theme = resolveTheme(themePreference, systemDark);
  const [wrap, setWrap] = useState(() => saved("wrap") === "true");
  const [diffTheme, setDiffTheme] = useState(() =>
    readDiffTheme(saved("diff-theme")),
  );
  const [lineDiffType, setLineDiffType] = useState(() =>
    readLineDiffType(saved("line-diff-type")),
  );
  useEffect(() => {
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setSystemDark(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => {
    save(THEME_STORAGE_KEY, themePreference);
  }, [themePreference]);
  useEffect(() => {
    save("layout", layout);
  }, [layout]);
  useEffect(() => {
    save("wrap", String(wrap));
  }, [wrap]);
  useEffect(() => {
    save("diff-theme", diffTheme);
  }, [diffTheme]);
  useEffect(() => {
    save("line-diff-type", lineDiffType);
  }, [lineDiffType]);
  const setLayout = useCallback(
    (value: "split" | "unified") => {
      if (mobile) setNarrowLayout(value);
      else setDesktopLayout(value);
    },
    [mobile],
  );
  const effectiveLayout = mobile ? (narrowLayout ?? "unified") : layout;
  return useMemo(
    () => ({
      theme,
      themePreference,
      setThemePreference,
      layout: effectiveLayout,
      setLayout,
      wrap,
      setWrap,
      diffTheme,
      setDiffTheme,
      lineDiffType,
      setLineDiffType,
    }),
    [
      theme,
      themePreference,
      effectiveLayout,
      setLayout,
      wrap,
      diffTheme,
      lineDiffType,
    ],
  );
}

const AppearanceContext = createContext<ReturnType<
  typeof useAppearanceState
> | null>(null);

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const { mobile } = useSidebarState();
  const appearance = useAppearanceState(mobile);
  return <AppearanceContext value={appearance}>{children}</AppearanceContext>;
}

export function useAppearance() {
  const appearance = useContext(AppearanceContext);
  if (!appearance)
    throw new Error("Page components require AppearanceProvider");
  return appearance;
}
