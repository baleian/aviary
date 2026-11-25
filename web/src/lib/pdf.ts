import { getDocument, GlobalWorkerOptions, type RenderTask } from "pdfjs-dist";

// PDF.js reads documents in a worker, which this module starts when it is
// first asked for: the web UI loads the module only to show a PDF.
GlobalWorkerOptions.workerPort = new Worker(
  new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url),
  { type: "module" },
);

// A page that is being drawn.
export interface Drawing {
  done: Promise<void>;
  cancel(): void;
}

// The pages of a PDF that is open.
export interface Pages {
  count: number;
  // How wide the first page is for its height.
  ratio: number;
  // draw draws a page, of which the first is 1, on the canvas, so many
  // pixels wide. done fails if the page cannot be drawn, and if cancel
  // stopped the drawing.
  draw(page: number, canvas: HTMLCanvasElement, width: number): Drawing;
  // close lets go of the document, and of what is being drawn of it.
  close(): void;
}

// Where the web UI serves what PDF.js reads when a document needs it: see
// vite.config.ts.
const DATA = "/pdfjs/";

// open reads a PDF. It fails if the file is not one that PDF.js can read.
export async function open(file: Blob): Promise<Pages> {
  const task = getDocument({
    data: new Uint8Array(await file.arrayBuffer()),
    cMapUrl: DATA + "cmaps/",
    standardFontDataUrl: DATA + "standard_fonts/",
    wasmUrl: DATA + "wasm/",
    // The content security policy of the web UI lets no WebAssembly be
    // compiled, so the decoders run as scripts.
    useWasm: false,
  });
  const document = await task.promise;
  const first = (await document.getPage(1)).getViewport({ scale: 1 });
  return {
    count: document.numPages,
    ratio: first.width / first.height,
    draw(number, canvas, width) {
      let rendering: RenderTask | undefined;
      let cancelled = false;
      const draw = async () => {
        const page = await document.getPage(number);
        if (cancelled) throw new Error("cancelled");
        const scale = width / page.getViewport({ scale: 1 }).width;
        const viewport = page.getViewport({ scale });
        canvas.width = Math.round(viewport.width);
        canvas.height = Math.round(viewport.height);
        rendering = page.render({ canvas, viewport });
        await rendering.promise;
      };
      return {
        done: draw(),
        cancel() {
          cancelled = true;
          rendering?.cancel();
        },
      };
    },
    close: () => void task.destroy(),
  };
}
