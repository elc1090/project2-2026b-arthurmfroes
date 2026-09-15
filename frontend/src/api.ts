export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
  ) {
    super(code);
  }
}
export async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const options: RequestInit = {
    method,
    credentials: "same-origin",
    signal,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  };
  let response: Response;
  try {
    response = await fetch(`/api${path}`, options);
  } catch (error) {
    if (error instanceof TypeError) throw new APIError(0, "network_error");
    throw error;
  }
  if (!response.ok) {
    const result = await response.json().catch(() => ({}));
    throw new APIError(response.status, result.error || "request_failed");
  }
  return response.status === 204 ? (undefined as T) : response.json();
}
export function message(error: unknown): string {
  if (error instanceof APIError) {
    if (error.status === 401)
      return "Sua sessão terminou. Entre novamente para continuar.";
    if (error.status === 409)
      return "Já existe um item com esse nome ou os dados não correspondem à operação.";
    if (error.status === 400) return "Confira os dados informados.";
    if (error.status === 404) return "Este item não está disponível.";
    if (error.status === 503)
      return "O serviço está temporariamente indisponível.";
  }
  return error instanceof Error
    ? error.message
    : "Não foi possível concluir a solicitação.";
}
export function sendPart(
  id: string,
  index: number,
  blob: Blob,
  signal: AbortSignal,
  progress: (bytes: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    signal.addEventListener("abort", abort, { once: true });
    const finish = (error?: Error) => {
      signal.removeEventListener("abort", abort);
      error ? reject(error) : resolve();
    };
    xhr.open("PUT", `/api/uploads/${encodeURIComponent(id)}/parts/${index}`);
    xhr.timeout = 120000;
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.upload.onprogress = (e) => progress(e.loaded);
    xhr.onload = () =>
      xhr.status >= 200 && xhr.status < 300
        ? finish()
        : finish(new APIError(xhr.status, "part_failed"));
    xhr.onerror = xhr.ontimeout = () =>
      finish(new APIError(0, "network_error"));
    xhr.onabort = () => finish(new DOMException("Aborted", "AbortError"));
    if (signal.aborted) finish(new DOMException("Aborted", "AbortError"));
    else xhr.send(blob);
  });
}
