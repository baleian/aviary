import type { FileRef } from "@/types";

export const ACCEPTED_IMAGE_TYPES = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
] as const;

export const MAX_IMAGE_SIZE = 5 * 1024 * 1024; // 5 MB
export const MAX_ATTACHMENTS = 10;

export function validateImageFile(file: File): string | null {
  if (!ACCEPTED_IMAGE_TYPES.includes(file.type as (typeof ACCEPTED_IMAGE_TYPES)[number])) {
    return `Unsupported file type: ${file.type || "unknown"}`;
  }
  if (file.size > MAX_IMAGE_SIZE) {
    return `File too large (${(file.size / 1024 / 1024).toFixed(1)} MB). Max: 5 MB`;
  }
  return null;
}

export async function uploadFile(file: File): Promise<FileRef> {
  const form = new FormData();
  form.append("file", file);
  const res = await fetch("/api/uploads", {
    method: "POST",
    credentials: "include",
    body: form,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({ detail: res.statusText }));
    throw new Error(body.detail || "Upload failed");
  }
  return res.json();
}

export function getUploadUrl(fileId: string): string {
  return `/api/uploads/${fileId}`;
}
