import { useCallback, useMemo, useState } from "react";
import { SquarePen, Brain, History, Settings as SettingsIcon } from "lucide-react";
import { useT } from "./lib/i18n";
import { useController } from "./lib/useController";
import { Transcript } from "./components/Transcript";
import { Composer } from "./components/Composer";
import { TodoPanel } from "./components/TodoPanel";
import { ApprovalModal } from "./components/ApprovalModal";
import { AskCard } from "./components/AskCard";
import { StatusBar } from "./components/StatusBar";
import { MemoryPanel } from "./components/MemoryPanel";
import { HistoryPanel } from "./components/HistoryPanel";
import { SettingsPanel } from "./components/SettingsPanel";
import { parseTodos } from "./lib/tools";
import type { MemoryView, SessionMeta } from "./lib/types";

export default function App() {
  const {
    state,
    send,
    cancel,
    approve,
    answerQuestion,
    setPlan,
    newSession,
    listSessions,
    resumeSession,
    deleteSession,
    renameSession,
    refreshMeta,
    pickWorkspace,
    setModel,
    fetchMemory,
    remember,
    saveDoc,
  } = useController();
  const t = useT();
  const [plan, setPlanLocal] = useState(false);
  const [memView, setMemView] = useState<MemoryView | null>(null);
  const [histView, setHistView] = useState<SessionMeta[] | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const togglePlan = () => {
    const next = !plan;
    setPlanLocal(next);
    setPlan(next);
  };

  // Switching models rebuilds the controller, which starts in normal mode — so
  // re-apply the current plan state, or the pill would say "plan on" while the
  // fresh controller silently lets writers through.
  const switchModel = useCallback(
    async (name: string) => {
      await setModel(name);
      if (plan) setPlan(true);
    },
    [setModel, plan, setPlan],
  );

  // The live task list pinned above the composer comes from the most recent
  // top-level todo_write call; it stays visible while work remains, clears itself
  // once every item is completed, and can be dismissed by the user (the ✕). A
  // dismissal is keyed to that list's id, so a fresh todo_write (a new task)
  // brings the panel back.
  const todoItem = useMemo(() => {
    for (let i = state.items.length - 1; i >= 0; i--) {
      const it = state.items[i];
      if (it.kind === "tool" && it.name === "todo_write" && !it.parentId) return it;
    }
    return null;
  }, [state.items]);
  const todos = useMemo(() => (todoItem ? parseTodos(todoItem.args) : []), [todoItem]);
  const [dismissedTodo, setDismissedTodo] = useState<string | null>(null);
  const showTodos =
    !!todoItem &&
    todoItem.id !== dismissedTodo &&
    todos.length > 0 &&
    todos.some((t) => t.status !== "completed");

  // Memory drawer: opening fetches a fresh snapshot; writes re-fetch so the
  // panel reflects what landed on disk.
  const openMemory = useCallback(async () => {
    setMemView(await fetchMemory());
  }, [fetchMemory]);

  const closeMemory = useCallback(() => setMemView(null), []);

  // History drawer: opening fetches the saved-session list; picking one resumes it
  // (the transcript swaps in; the model/folder are unchanged).
  const openHistory = useCallback(async () => {
    setHistView(await listSessions());
  }, [listSessions]);
  const closeHistory = useCallback(() => setHistView(null), []);
  const onResumeSession = useCallback(
    async (path: string) => {
      setHistView(null);
      await resumeSession(path);
    },
    [resumeSession],
  );
  // Delete / rename act on disk, then re-fetch so the panel reflects the change.
  const onDeleteSession = useCallback(
    async (path: string) => {
      await deleteSession(path);
      setHistView(await listSessions());
    },
    [deleteSession, listSessions],
  );
  const onRenameSession = useCallback(
    async (path: string, title: string) => {
      await renameSession(path, title);
      setHistView(await listSessions());
    },
    [renameSession, listSessions],
  );

  // Workspace: open the folder chooser and switch projects (from the status bar's
  // folder button). The hook resets the transcript and refreshes meta on a pick; a
  // cancel is a no-op.
  const switchFolder = useCallback(async () => {
    await pickWorkspace();
  }, [pickWorkspace]);

  const onRemember = useCallback(
    async (scope: string, note: string) => {
      await remember(scope, note);
      setMemView(await fetchMemory());
    },
    [remember, fetchMemory],
  );

  const onSaveDoc = useCallback(
    async (path: string, body: string) => {
      await saveDoc(path, body);
      setMemView(await fetchMemory());
    },
    [saveDoc, fetchMemory],
  );

  return (
    <div className="app">
      <header className="topbar">
        <span className="topbar__model">{state.meta?.label ?? "…"}</span>
        <div className="topbar__spacer" />
        <button
          className="chip chip--icon"
          onClick={() => void openHistory()}
          disabled={state.running}
          title={state.running ? t("common.busyHint") : t("topbar.history")}
        >
          <History size={13} />
        </button>
        <button className="chip chip--icon" onClick={() => void openMemory()} title={t("topbar.memory")}>
          <Brain size={13} />
        </button>
        <button
          className="chip chip--icon"
          onClick={() => setSettingsOpen(true)}
          disabled={state.running}
          title={state.running ? t("common.busyHint") : t("topbar.settings")}
        >
          <SettingsIcon size={13} />
        </button>
        <button className="chip chip--icon" onClick={newSession} title={t("topbar.newSession")}>
          <SquarePen size={13} />
        </button>
      </header>

      {state.meta?.startupErr && (
        <div className="banner banner--error">{t("topbar.startupError", { msg: state.meta.startupErr })}</div>
      )}

      <main className="main">
        <Transcript items={state.items} onPrompt={send} />
      </main>

      <footer className="footer">
        {showTodos && <TodoPanel todos={todos} onDismiss={() => setDismissedTodo(todoItem!.id)} />}
        <Composer running={state.running} plan={plan} onSend={send} onCancel={cancel} onTogglePlan={togglePlan} />
        <StatusBar
          meta={state.meta}
          context={state.context}
          usage={state.usage}
          balance={state.balance}
          running={state.running}
          plan={plan}
          turnStartAt={state.turnStartAt}
          turnTokens={state.turnTokens}
          onSwitchModel={switchModel}
          onPickFolder={() => void switchFolder()}
        />
      </footer>

      {state.approval && (
        <ApprovalModal
          approval={state.approval}
          onAnswer={(allow, session) => {
            // Approving an exit_plan_mode plan leaves plan mode (the controller
            // flips the executor; mirror it here for the indicator).
            if (state.approval!.tool === "exit_plan_mode" && allow) setPlanLocal(false);
            approve(state.approval!.id, allow, session);
          }}
        />
      )}

      {state.ask && (
        <AskCard
          ask={state.ask}
          onAnswer={answerQuestion}
          onDismiss={() => answerQuestion(state.ask!.id, [])}
        />
      )}

      {memView !== null && (
        <MemoryPanel
          view={memView}
          onClose={closeMemory}
          onRemember={onRemember}
          onSaveDoc={onSaveDoc}
        />
      )}

      {histView !== null && (
        <HistoryPanel
          sessions={histView}
          onResume={onResumeSession}
          onDelete={onDeleteSession}
          onRename={onRenameSession}
          onClose={closeHistory}
        />
      )}

      {settingsOpen && <SettingsPanel onClose={() => setSettingsOpen(false)} onChanged={() => void refreshMeta()} />}
    </div>
  );
}
