import { useEffect, useState } from "react";
import { FolderGit2, Wallet } from "lucide-react";
import { ModelSwitcher } from "./ModelSwitcher";
import { SPINNER_WORDS, useI18n } from "../lib/i18n";
import type { BalanceInfo, ContextInfo, Meta, WireUsage } from "../lib/types";

// cacheRate is the prompt cache-hit percentage from the last turn's usage, or
// null when there's nothing to show. Mirrors the kernel's cached/new accounting:
// prefer hit/(hit+miss), falling back to hit/prompt when only hits are reported.
function cacheRate(u?: WireUsage): number | null {
  if (!u) return null;
  let denom = u.cacheHitTokens + u.cacheMissTokens;
  if (denom === 0) denom = u.promptTokens;
  if (denom <= 0) return null;
  return Math.round((u.cacheHitTokens / denom) * 100);
}

// shortCwd trims a path to its last two segments so the status line stays compact
// (e.g. /Users/x/projects/reasonix → …/projects/reasonix).
function shortCwd(cwd: string): string {
  const parts = cwd.split("/").filter(Boolean);
  if (parts.length <= 2) return cwd;
  return "…/" + parts.slice(-2).join("/");
}

function fmtTokens(n: number): string {
  if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, "") + "k";
  return String(n);
}

function fmtElapsed(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

// useTick re-renders once a second while `on`, so the elapsed clock advances.
function useTick(on: boolean): number {
  const [, setN] = useState(0);
  useEffect(() => {
    if (!on) return;
    const id = setInterval(() => setN((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [on]);
  return Date.now();
}

export function StatusBar({
  meta,
  context,
  usage,
  balance,
  running,
  plan,
  turnStartAt,
  turnTokens,
  onSwitchModel,
  onPickFolder,
}: {
  meta?: Meta;
  context: ContextInfo;
  usage?: WireUsage;
  balance?: BalanceInfo;
  running: boolean;
  plan: boolean;
  turnStartAt: number;
  turnTokens: number;
  onSwitchModel: (name: string) => void;
  onPickFolder: () => void;
}) {
  const { t, locale } = useI18n();
  const now = useTick(running);
  const pct = context.window ? Math.min(100, Math.round((context.used / context.window) * 100)) : null;
  const cachePct = cacheRate(usage);

  // While a turn runs, the status line shows live activity (word · elapsed ·
  // tokens) in place of the static context gauge.
  let activity: string | null = null;
  if (running && turnStartAt) {
    const elapsedMs = Math.max(0, now - turnStartAt);
    const words = SPINNER_WORDS[locale];
    const word = words[Math.floor(elapsedMs / 3000) % words.length];
    const tok = turnTokens > 0 ? ` · ↓ ${fmtTokens(turnTokens)} ${t("status.tokens")}` : "";
    activity = `${word}… ${fmtElapsed(elapsedMs)}${tok}`;
  }

  return (
    <div className="statusbar">
      <span className={`statusbar__dot ${running ? "statusbar__dot--busy" : ""}`} />
      <ModelSwitcher label={meta?.label ?? t("status.connecting")} onPick={onSwitchModel} />
      {activity ? (
        <>
          <span className="statusbar__sep">·</span>
          <span className="statusbar__activity">{activity}</span>
        </>
      ) : (
        pct !== null && (
          <>
            <span className="statusbar__sep">·</span>
            <span className="statusbar__ctx">{t("status.ctx", { pct })}</span>
          </>
        )
      )}
      {cachePct !== null && (
        <>
          <span className="statusbar__sep">·</span>
          <span className="statusbar__cache">{t("status.cache", { pct: cachePct })}</span>
        </>
      )}
      {balance?.available && balance.display && (
        <>
          <span className="statusbar__sep">·</span>
          <span className="statusbar__balance" title={t("status.balanceTitle")}>
            <Wallet size={11} />
            {balance.display}
          </span>
        </>
      )}
      {meta?.cwd && (
        <>
          <span className="statusbar__sep">·</span>
          <button
            className="statusbar__cwd"
            onClick={onPickFolder}
            disabled={running}
            title={running ? t("common.busyHint") : t("status.switchFolder", { cwd: meta.cwd })}
          >
            <FolderGit2 size={11} />
            {shortCwd(meta.cwd)}
          </button>
        </>
      )}
      <span className="statusbar__spacer" />
      {plan && <span className="statusbar__plan">{t("status.plan")}</span>}
    </div>
  );
}
