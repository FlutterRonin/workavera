import { ClientResponseError, type RecordModel } from "pocketbase"

import { pb } from "@/lib/pocketbase"

export const RESOURCES_COLLECTION = "user_resources"
export const RESOURCE_LEASES_COLLECTION = "resource_download_leases"
export const RESOURCE_MAX_SIZE = 6 * 1024 * 1024
export const MAX_MESSAGE_RESOURCES = 6

/** Every extension the server accepts. `accept` is only a hint: the server
 * still detects the real media type from the content. */
export const RESOURCE_EXTENSIONS = [
  "txt",
  "md",
  "json",
  "jsonl",
  "pdf",
  "docx",
  "docm",
  "doc",
  "pptx",
  "pptm",
  "ppsx",
  "ppsm",
  "ppt",
  "pps",
  "pot",
  "xlsx",
  "xlsm",
  "xlsb",
  "xls",
  "odt",
  "ods",
  "odp",
  "rtf",
  "epub",
  "csv",
  "html",
  "htm",
  "png",
  "jpg",
  "jpeg",
  "gif",
  "webp",
  "zip",
]

/** Media types stored without text: they can be downloaded but have no
 * extracted text to view, search, or read. */
const BINARY_MEDIA_TYPES = new Set([
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "application/zip",
])

export function isBinaryResource(mediaType: string) {
  return BINARY_MEDIA_TYPES.has(mediaType)
}

export const RESOURCE_ACCEPT = RESOURCE_EXTENSIONS.map((ext) => `.${ext}`).join(
  ","
)

/** Fields a listing needs; converted_markdown can be 8 MB and is never listed. */
export const RESOURCE_LIST_FIELDS =
  "id,display_name,original_name,media_type,size,file,url,created,updated"

export type Resource = {
  id: string
  display_name: string
  original_name: string
  media_type: string
  size: number
  file: string
  url: string
  created: string
  updated: string
}

export type ResourceRecord = RecordModel & Resource

export type ResourceLease = {
  id: string
  resource: string
  purpose: string
  expires_at: string
  created: string
}

const ERROR_MESSAGES: Record<string, string> = {
  file_required: "Choose a file to upload.",
  file_empty: "The file is empty.",
  file_too_large: "Files must be 6 MB or smaller.",
  unsupported_media_type:
    "This file type is not supported. Upload PDF, Office, OpenDocument, RTF, EPUB, CSV, HTML, text, Markdown, JSON, PNG, JPEG, GIF, WebP, or ZIP files.",
  unsupported_encoding: "The file's text encoding is not supported.",
  invalid_filename: "The file name is missing or invalid.",
  conversion_failed: "The document could not be converted to text.",
  conversion_timeout: "Converting the document took too long.",
  document_encrypted: "The document is password-protected.",
  document_malformed: "The document is damaged or incomplete.",
  document_needs_ocr:
    "This PDF contains scanned pages without extractable text.",
  converted_content_empty: "No text could be extracted from this file.",
  converted_content_too_large: "The converted text is larger than 8 MB.",
  resource_not_found: "This resource no longer exists.",
}

/** Explains a failed resource request from its stable error code. The
 * server's own message is preferred when it is more specific, such as the
 * pages of a PDF that need OCR. */
export function resourceErrorMessage(error: unknown, fallback: string) {
  if (error instanceof ClientResponseError) {
    const code = error.response?.code as string | undefined
    if (code === "document_needs_ocr" && error.response?.message) {
      return error.response.message as string
    }
    if (code && ERROR_MESSAGES[code]) return ERROR_MESSAGES[code]
    if (error.status === 413) return ERROR_MESSAGES.file_too_large
    return (error.response?.message as string) || fallback
  }
  return error instanceof Error ? error.message : fallback
}

export function fileExtension(name: string) {
  const index = name.lastIndexOf(".")
  return index > 0 ? name.slice(index + 1).toLowerCase() : ""
}

// Tints follow the colors people already associate with each family of
// files, so a glance at the tile tells a PDF from a spreadsheet.
const tileTones = {
  pdf: "bg-red-500/10 text-red-600 dark:text-red-400",
  word: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  sheet: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
  slides: "bg-orange-500/10 text-orange-600 dark:text-orange-400",
  code: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
  text: "bg-muted text-muted-foreground",
}

/** Tailwind classes that tint a file-type icon by its family. */
export function resourceTypeTone(mediaType: string, name: string) {
  const ext = fileExtension(name)
  if (mediaType === "application/pdf") return tileTones.pdf
  if (
    mediaType.includes("word") ||
    mediaType.includes("opendocument.text") ||
    ["rtf", "epub"].includes(ext)
  )
    return tileTones.word
  if (
    mediaType.includes("sheet") ||
    mediaType.includes("excel") ||
    mediaType === "text/csv"
  )
    return tileTones.sheet
  if (mediaType.includes("presentation") || mediaType.includes("powerpoint"))
    return tileTones.slides
  if (mediaType === "text/html" || mediaType.includes("json"))
    return tileTones.code
  return tileTones.text
}

/** Checks what can be checked before uploading; the server checks again. */
export function validateResourceFile(file: File): string | null {
  if (!RESOURCE_EXTENSIONS.includes(fileExtension(file.name))) {
    return ERROR_MESSAGES.unsupported_media_type
  }
  if (file.size === 0) return ERROR_MESSAGES.file_empty
  if (file.size > RESOURCE_MAX_SIZE) return ERROR_MESSAGES.file_too_large
  return null
}

export async function uploadResource(file: File): Promise<Resource> {
  const body = new FormData()
  body.set("file", file)
  return pb.send<Resource>("/api/resources", {
    method: "POST",
    body,
    requestKey: null,
  })
}

const FORMAT_LABELS: Record<string, string> = {
  "application/pdf": "PDF",
  "text/plain": "Text",
  "text/markdown": "Markdown",
  "application/json": "JSON",
  "application/x-ndjson": "JSONL",
  "text/csv": "CSV",
  "text/html": "HTML",
  "application/rtf": "RTF",
  "application/epub+zip": "EPUB",
  "application/msword": "Word",
  "application/vnd.ms-powerpoint": "PowerPoint",
  "application/vnd.ms-excel": "Excel",
  "application/vnd.oasis.opendocument.text": "ODT",
  "application/vnd.oasis.opendocument.spreadsheet": "ODS",
  "application/vnd.oasis.opendocument.presentation": "ODP",
}

/** A short human label for a media type, such as "PDF" or "Word". */
export function formatLabel(mediaType: string) {
  if (FORMAT_LABELS[mediaType]) return FORMAT_LABELS[mediaType]
  if (mediaType.includes("word")) return "Word"
  if (mediaType.includes("presentation") || mediaType.includes("powerpoint"))
    return "PowerPoint"
  if (mediaType.includes("sheet") || mediaType.includes("excel")) return "Excel"
  return mediaType.split("/").pop()?.toUpperCase() ?? "File"
}

/** Formats a byte count with 1024-based units, such as "2.4 MB". */
export function formatBytes(size: number) {
  if (!Number.isFinite(size) || size < 1024) return `${Math.max(0, size)} B`
  const units = ["KB", "MB", "GB"]
  let value = size / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value >= 10 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`
}

/** Opens the protected source file in a new tab, or downloads it. The URL
 * needs a short-lived PocketBase file token, so the tab is opened first to
 * keep the browser from treating it as an unsolicited popup. */
export async function openResourceFile(
  resource: Pick<Resource, "id" | "file">,
  download = false
) {
  const popup = window.open("about:blank", "_blank")
  if (popup) popup.opener = null
  try {
    const token = await pb.files.getToken({ requestKey: null })
    const url = pb.files.getURL(
      { id: resource.id, collectionName: RESOURCES_COLLECTION },
      resource.file,
      { token, download }
    )
    if (popup) {
      popup.location.href = url
    } else {
      window.location.assign(url)
    }
  } catch (error) {
    popup?.close()
    throw error
  }
}

const resourceCache = new Map<string, Promise<Resource | null>>()

/** Loads the current state of a resource referenced by a past message, or
 * null when it has been deleted. Results are cached per session, so a long
 * conversation does not request the same resource repeatedly. */
export function getResourceState(id: string): Promise<Resource | null> {
  const key = `${pb.authStore.record?.id ?? ""}:${id}`
  let request = resourceCache.get(key)
  if (!request) {
    request = pb
      .collection(RESOURCES_COLLECTION)
      .getOne<ResourceRecord>(id, {
        fields: RESOURCE_LIST_FIELDS,
        requestKey: null,
      })
      .then((record) => record as Resource)
      .catch((error: unknown) => {
        if (error instanceof ClientResponseError && error.status === 404) {
          return null
        }
        resourceCache.delete(key)
        throw error
      })
    resourceCache.set(key, request)
  }
  return request
}

/** Drops cached resource state after the library changes. */
export function forgetResourceState(id?: string) {
  if (!id) {
    resourceCache.clear()
    return
  }
  for (const key of resourceCache.keys()) {
    if (key.endsWith(`:${id}`)) resourceCache.delete(key)
  }
}
