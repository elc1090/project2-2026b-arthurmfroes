import { prepare } from "./hash";
self.onmessage = async (event: MessageEvent<{ file: File }>) => {
  try {
    const manifest = await prepare(event.data.file, (bytes) =>
      self.postMessage({ bytes }),
    );
    self.postMessage({ manifest });
  } catch {
    self.postMessage({ error: "Não foi possível ler o arquivo local." });
  }
};
