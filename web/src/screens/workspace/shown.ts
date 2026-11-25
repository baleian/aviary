import { useQuery } from "@tanstack/react-query";
import { type RefObject, useEffect, useState } from "react";
import { api } from "../../api/client.ts";
import type { Attachment } from "../../api/types.ts";
import { type Kind, kindOf } from "../../lib/files.ts";
import { thumbnail } from "../../lib/thumbnail.ts";

// A file as the workspace shows it: one that a message carries, or one that
// is chosen for the message that is being written.
export interface Shown {
  // Tells the file from every other that is shown.
  key: string;
  name: string;
  // In bytes.
  size: number;
  kind: Kind;
  // read returns the file, or no more than its first bytes if it is told
  // how many.
  read: (first?: number) => Promise<Blob>;
}

// carriedFile returns the file of an attachment, which the API has.
export function carriedFile(attachment: Attachment): Shown {
  return {
    key: attachment.id,
    name: attachment.name,
    size: attachment.size,
    kind: kindOf(attachment.name, attachment.media_type),
    read: (first) => api.download("/v1/attachments/" + attachment.id, first),
  };
}

// chosenKey returns what tells a file that was chosen in the browser from
// every other, from the number that it was chosen as.
export const chosenKey = (key: number) => "chosen-" + key;

// chosenFile returns a file that was chosen in the browser.
export function chosenFile(key: number, file: File): Shown {
  return {
    key: chosenKey(key),
    name: file.name,
    size: file.size,
    kind: kindOf(file.name, file.type),
    read: (first) => Promise.resolve(file.slice(0, first, file.type)),
  };
}

// thumbnailKey names the thumbnail of a file among what has been read.
export const thumbnailKey = (key: string) => ["thumbnail", key];

// The largest file that is read for its thumbnail, in bytes.
const LARGEST = 16 << 20;

// useThumbnail returns a small picture of the file once there is one, as an
// address to show. The file is read for it only if reads says so, and not
// again after that.
export function useThumbnail(file: Shown, reads: boolean): string | undefined {
  const { data } = useQuery({
    queryKey: thumbnailKey(file.key),
    queryFn: async () => thumbnail(await file.read(), file.kind),
    enabled: reads && file.kind.view === "image" && file.size <= LARGEST,
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
  });
  return data ?? undefined;
}

// useSeen tells whether the element has come near what the window shows,
// and stays true once it has.
export function useSeen(element: RefObject<Element | null>): boolean {
  const [seen, setSeen] = useState(typeof IntersectionObserver === "undefined");
  useEffect(() => {
    const node = element.current;
    if (!node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return;
        observer.disconnect();
        setSeen(true);
      },
      { rootMargin: "240px" },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [element]);
  return seen;
}
