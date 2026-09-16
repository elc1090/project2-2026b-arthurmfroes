import React, { useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { api, APIError, message } from "./api";
import {
  matchReselections,
  transferLabels,
  UploadQueue,
  type Transfer,
} from "./queue";
import type { Folder, Listing, User } from "./types";
import { confirmFileDeletion } from "./file-actions";
import "./style.css";
import { Admin } from "./Admin";
import { FileActions } from "./FileActions";
export function bytes(n: number) {
  if (n === 0) return "0 B";
  const i = Math.min(4, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / 1024 ** i).toLocaleString("pt-BR", { maximumFractionDigits: 1 })} ${["B", "KiB", "MiB", "GiB", "TiB"][i]}`;
}
function Mark() {
  return (
    <span className="mark" aria-hidden="true">
      <i />
      <i />
      <i />
    </span>
  );
}
function App() {
  const [user, setUser] = useState<User | null>(null),
    [loading, setLoading] = useState(true),
    [error, setError] = useState("");
  useEffect(() => {
    api<User>("/me")
      .then(setUser)
      .catch((e) => {
        if (!(e instanceof APIError && e.status === 401)) setError(message(e));
      })
      .finally(() => setLoading(false));
  }, []);
  if (loading) return <main className="loading">Abrindo seu Acervo…</main>;
  return user ? (
    <Drive key={user.id} user={user} exit={() => setUser(null)} />
  ) : (
    <Login
      enter={(authenticated) => {
        setError("");
        setUser(authenticated);
      }}
      initialError={error}
    />
  );
}
function Login({
  enter,
  initialError,
}: {
  enter: (u: User) => void;
  initialError: string;
}) {
  const [register, setRegister] = useState(false),
    [error, setError] = useState(initialError),
    [busy, setBusy] = useState(false);
  return (
    <main className="login">
      <section className="intro">
        <div className="brand">
          <Mark /> Acervo
        </div>
        <h1>
          Um lugar para
          <br />o que você guarda.
        </h1>
        <p>
          Organize seus arquivos em pastas e acompanhe cada transferência até a
          confirmação.
        </p>
        <span className="intro-note">Seus arquivos. Seu espaço.</span>
      </section>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          const data = new FormData(e.currentTarget);
          const body = {
            login: data.get("login"),
            password: data.get("password"),
          };
          try {
            if (register) await api("/register", "POST", body);
            const result = await api<{ user: User }>("/login", "POST", body);
            enter(result.user);
          } catch (e) {
            setError(message(e));
          } finally {
            setBusy(false);
          }
        }}
      >
        <span className="eyebrow">BEM-VINDO AO ACERVO</span>
        <h2>{register ? "Crie seu espaço" : "Entre no seu espaço"}</h2>
        <p>Use seu login e senha para continuar.</p>
        <label>
          Login
          <input name="login" required autoComplete="username" autoFocus />
        </label>
        <label>
          Senha
          <input
            name="password"
            type="password"
            required
            autoComplete={register ? "new-password" : "current-password"}
          />
        </label>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <button className="primary" disabled={busy}>
          {busy ? "Aguarde…" : register ? "Criar conta" : "Entrar"}
        </button>
        <button
          type="button"
          className="text-button"
          onClick={() => {
            setRegister(!register);
            setError("");
          }}
        >
          {register ? "Já tenho uma conta" : "Ainda não tenho uma conta"}
        </button>
      </form>
    </main>
  );
}
function Drive({ user, exit }: { user: User; exit: () => void }) {
  const [path, setPath] = useState<Folder[]>([]),
    [listing, setListing] = useState<Listing>({
      folder: null,
      folders: [],
      files: [],
    }),
    [error, setError] = useState(""),
    [refresh, setRefresh] = useState(0),
    [pending, setPending] = useState(true),
    [deletingID, setDeletingID] = useState<string | null>(null),
    [folderDialog, setFolderDialog] = useState(false),
    [admin, setAdmin] = useState(false);
  const [, redraw] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const resume = useRef<HTMLInputElement>(null);
  const selected = useRef<Transfer | null>(null);
  const queueRef = useRef<UploadQueue | null>(null);
  const owner = useRef(user.id);
  const deletion = useRef<AbortController | null>(null);
  if (!queueRef.current)
    queueRef.current = new UploadQueue(
      owner.current,
      () => redraw((n) => n + 1),
      exit,
    );
  const queue = queueRef.current;
  const folderID = path.at(-1)?.id || null;
  useEffect(() => {
    queue.restore().catch((e) => setError(message(e)));
    return () => {
      deletion.current?.abort();
      queue.dispose();
    };
  }, [queue]);
  const completed = queue.rows.filter((r) => r.state === "complete").length;
  useEffect(() => {
    const abort = new AbortController();
    setPending(true);
    setError("");
    api<Listing>(
      `/folders${folderID ? `?parent_id=${encodeURIComponent(folderID)}` : ""}`,
      "GET",
      undefined,
      abort.signal,
    )
      .then(setListing)
      .catch((e) => {
        if (!abort.signal.aborted) {
          if (e instanceof APIError && e.status === 401) exit();
          else setError(message(e));
        }
      })
      .finally(() => {
        if (!abort.signal.aborted) setPending(false);
      });
    return () => abort.abort();
  }, [folderID, refresh, completed]);
  async function removeFile(file: Listing["files"][number]) {
    if (deletion.current) return;
    const controller = new AbortController();
    try {
      const confirmed = await confirmFileDeletion(
        file,
        window.confirm,
        controller.signal,
        () => {
          deletion.current = controller;
          setDeletingID(file.id);
          setError("");
        },
      );
      if (!confirmed) return;
      setListing((current) => ({
        ...current,
        files: current.files.filter((item) => item.id !== file.id),
      }));
      queue.removeCompletedFile(file.id);
      setRefresh((value) => value + 1);
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) exit();
      else setError(`Não foi possível excluir o arquivo. ${message(cause)}`);
    } finally {
      if (deletion.current === controller) {
        deletion.current = null;
        setDeletingID(null);
      }
    }
  }
  return (
    <div className="shell">
      <aside>
        <div className="brand">
          <Mark /> Acervo
        </div>
        <button className="primary add" onClick={() => input.current?.click()}>
          ＋ Enviar arquivos
        </button>
        <nav aria-label="Áreas">
          <button
            className={!admin ? "active" : ""}
            onClick={() => setAdmin(false)}
          >
            ▤ <span>Meus arquivos</span>
          </button>
          {user.is_admin && (
            <button
              className={admin ? "active" : ""}
              onClick={() => setAdmin(true)}
            >
              ◎ <span>Administração</span>
            </button>
          )}
        </nav>
        <div className="side-note">
          Tudo em seu lugar.
          <br />
          <small>Transferências continuam enquanto você navega.</small>
        </div>
        <div className="account">
          <span className="avatar">{user.login.slice(0, 1).toUpperCase()}</span>
          <span>{user.login}</span>
          <button
            title="Sair"
            onClick={async () => {
              try {
                await api("/logout", "POST");
                queue.dispose();
                exit();
              } catch (e) {
                setError(message(e));
              }
            }}
          >
            Sair
          </button>
        </div>
      </aside>
      <main className="workspace">
        <header>
          <span className="eyebrow">SEU ESPAÇO</span>
          <span className="private">● Privado</span>
        </header>
        {admin ? (
          <Admin expired={exit} owner={user.id} />
        ) : (
          <>
            <div className="title-row">
              <div>
                <h1>Meus arquivos</h1>
                <p>Guarde, organize e encontre o que precisa.</p>
              </div>
              <button onClick={() => setFolderDialog(true)}>
                ＋ Nova pasta
              </button>
            </div>
            <div className="directory-toolbar">
              <nav className="breadcrumbs" aria-label="Caminho atual">
                <button onClick={() => setPath([])}>Meus arquivos</button>
                {path.map((folder, i) => (
                  <React.Fragment key={folder.id}>
                    <span>/</span>
                    <button
                      onClick={() => setPath(path.slice(0, i + 1))}
                      aria-current={i === path.length - 1 ? "page" : undefined}
                    >
                      {folder.name}
                    </button>
                  </React.Fragment>
                ))}
              </nav>
              <button
                className="text-button"
                onClick={() => setRefresh((n) => n + 1)}
              >
                Atualizar
              </button>
            </div>
            {error && (
              <p className="error" role="alert">
                {error}
              </p>
            )}
            <section className="file-list" aria-busy={pending}>
              <div className="list-head">
                <span>Nome</span>
                <span>Tamanho</span>
                <span>Adicionado em</span>
                <span />
              </div>
              {pending ? (
                <p className="empty">Carregando arquivos…</p>
              ) : listing.folders.length + listing.files.length === 0 ? (
                <div className="empty">
                  <div className="empty-icon">▱</div>
                  <h2>Espaço para suas ideias</h2>
                  <p>
                    Envie seus primeiros arquivos ou crie uma pasta para
                    começar.
                  </p>
                  <button onClick={() => input.current?.click()}>
                    Enviar arquivos
                  </button>
                </div>
              ) : (
                <>
                  {listing.folders.map((folder) => (
                    <div className="file-row" key={folder.id}>
                      <button
                        className="file-name"
                        onClick={() => setPath([...path, folder])}
                      >
                        <span className="folder-icon">▰</span>
                        {folder.name}
                      </button>
                      <span>—</span>
                      <span>
                        {new Date(folder.created_at).toLocaleDateString(
                          "pt-BR",
                        )}
                      </span>
                      <span />
                    </div>
                  ))}
                  {listing.files.map((file) => (
                    <div className="file-row" key={file.id}>
                      <span className="file-name">
                        <span className="document-icon">▤</span>
                        {file.name}
                      </span>
                      <span>{bytes(file.size)}</span>
                      <span>
                        {new Date(file.published_at).toLocaleDateString(
                          "pt-BR",
                        )}
                      </span>
                      <FileActions
                        file={file}
                        deleting={deletingID === file.id}
                        deletionPending={deletingID !== null}
                        onDelete={() => void removeFile(file)}
                      />
                    </div>
                  ))}
                </>
              )}
            </section>
          </>
        )}
        {queue.rows.length > 0 && (
          <section className="transfers">
            <div className="transfer-heading">
              <h2>
                Transferências <span>{queue.rows.length}</span>
              </h2>
              <button
                onClick={() => {
                  selected.current = null;
                  resume.current?.click();
                }}
              >
                Selecionar arquivos para retomar
              </button>
            </div>
            <p className="muted">
              O arquivo fica disponível depois da confirmação do armazenamento.
            </p>
            {queue.rows.map((row) => (
              <div className="transfer" key={row.key}>
                <div className="transfer-name">
                  <strong>{row.name}</strong>
                  <span>{bytes(row.size)}</span>
                </div>
                <div className={`status ${row.state}`}>
                  <span>{transferLabels[row.state]}</span>
                  {["preparing", "sending", "recovering", "confirming"].includes(
                    row.state,
                  ) && <span>{Math.round(row.progress * 100)}%</span>}
                </div>
                <progress
                  value={row.progress}
                  max={1}
                  aria-label={`Progresso de ${row.name}`}
                />
                {row.error && <p className="error">{row.error}</p>}
                <div className="transfer-actions">
                  {["reselect", "error"].includes(row.state) &&
                    row.operation &&
                    !row.cancelRequested && (
                      <button
                        onClick={() => {
                          selected.current = row;
                          resume.current?.click();
                        }}
                      >
                        Selecionar original
                      </button>
                    )}
                  {row.state === "error" && (
                    <button
                      disabled={row.cancelling}
                      onClick={() => queue.retry(row)}
                    >
                      Tentar novamente
                    </button>
                  )}
                  {!["complete", "cancelled"].includes(row.state) && (
                    <button
                      disabled={row.cancelling}
                      onClick={() => void queue.cancel(row)}
                    >
                      {row.cancelling ? "Cancelando…" : "Cancelar"}
                    </button>
                  )}
                  {row.state === "complete" && row.operation?.file_id && (
                    <a
                      href={`/api/files/${row.operation.file_id}/download`}
                      download={row.name}
                    >
                      Baixar arquivo
                    </a>
                  )}
                </div>
              </div>
            ))}
          </section>
        )}
      </main>
      <input
        ref={input}
        className="hidden"
        type="file"
        multiple
        onChange={(e) => {
          queue.add(Array.from(e.target.files || []), folderID);
          e.target.value = "";
        }}
      />
      <input
        ref={resume}
        className="hidden"
        type="file"
        multiple
        onChange={(e) => {
          const files = Array.from(e.target.files || []);
          e.target.value = "";
          if (selected.current) {
            if (files.length !== 1) {
              setError(
                "Selecione somente o arquivo original desta transferência.",
              );
              return;
            }
            void queue.reselect(selected.current, files[0]);
            selected.current = null;
            return;
          }
          const result = matchReselections(queue.rows, files);
          for (const { row, file } of result.matches)
            void queue.reselect(row, file);
          setError(result.errors.join(" "));
        }}
      />
      {folderDialog && (
        <div className="modal-backdrop">
          <form
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="folder-title"
            onSubmit={async (e) => {
              e.preventDefault();
              const name = new FormData(e.currentTarget).get("name");
              try {
                await api("/folders", "POST", { parent_id: folderID, name });
                setFolderDialog(false);
                setRefresh((n) => n + 1);
              } catch (e) {
                setError(message(e));
                setFolderDialog(false);
              }
            }}
          >
            <h2 id="folder-title">Nova pasta</h2>
            <label>
              Nome da pasta
              <input name="name" required autoFocus />
            </label>
            <div>
              <button type="button" onClick={() => setFolderDialog(false)}>
                Cancelar
              </button>
              <button className="primary">Criar pasta</button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}
createRoot(document.getElementById("root")!).render(<App />);
