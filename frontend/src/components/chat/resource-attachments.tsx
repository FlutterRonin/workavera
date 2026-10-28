import { HugeiconsIcon } from "@hugeicons/react"
import { Cancel01Icon, RefreshIcon } from "@hugeicons/core-free-icons"

import { ResourceIcon } from "@/components/resources/resource-icon"
import { Spinner } from "@/components/ui/spinner"
import {
  formatBytes,
  formatLabel,
  MAX_MESSAGE_RESOURCES,
  type Resource,
} from "@/lib/resources"
import { cn } from "@/lib/utils"

import type { InputAttachment } from "./use-resource-attachments"

export function AttachmentChip({
  attachment,
  onRemove,
  onRetry,
}: {
  attachment: InputAttachment
  onRemove: () => void
  onRetry: () => void
}) {
  const failed = attachment.status === "error"
  return (
    <div
      className={cn(
        "flex max-w-64 min-w-0 items-center gap-2 rounded-lg border bg-background px-2 py-1.5 text-xs",
        failed && "border-destructive/40 bg-destructive/5"
      )}
      title={failed ? attachment.error : attachment.name}
    >
      {attachment.status === "uploading" ? (
        <Spinner className="size-4 shrink-0" />
      ) : (
        <ResourceIcon
          mediaType={attachment.mediaType}
          className={failed ? "text-destructive" : "text-muted-foreground"}
        />
      )}
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium">{attachment.name}</div>
        <div
          className={cn(
            "truncate",
            failed ? "text-destructive" : "text-muted-foreground"
          )}
        >
          {attachment.status === "uploading"
            ? "Uploading…"
            : failed
              ? attachment.error
              : `${formatLabel(attachment.mediaType)} · ${formatBytes(attachment.size)}`}
        </div>
      </div>
      {failed && attachment.file && (
        <button
          type="button"
          className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={onRetry}
          aria-label={`Retry uploading ${attachment.name}`}
        >
          <HugeiconsIcon
            icon={RefreshIcon}
            strokeWidth={2}
            className="size-3.5"
          />
        </button>
      )}
      <button
        type="button"
        className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
        onClick={onRemove}
        aria-label={`Remove ${attachment.name}`}
      >
        <HugeiconsIcon
          icon={Cancel01Icon}
          strokeWidth={2}
          className="size-3.5"
        />
      </button>
    </div>
  )
}

export function ResourcePickerList({
  query,
  results,
  loading,
  highlighted,
  full,
  attachedIds,
  onHighlight,
  onSelect,
}: {
  query: string
  results: Resource[]
  loading: boolean
  highlighted: number
  full: boolean
  attachedIds: Set<string>
  onHighlight: (index: number) => void
  onSelect: (resource: Resource) => void
}) {
  let empty: string | null = null
  if (full) {
    empty = `A message can attach at most ${MAX_MESSAGE_RESOURCES} files.`
  } else if (!loading && results.length === 0) {
    empty = query
      ? "No resources match this name."
      : "No resources yet. Upload a file with the + button."
  }

  return (
    <div
      className="absolute bottom-full left-0 z-20 mb-2 w-full max-w-md overflow-hidden rounded-lg border bg-popover text-popover-foreground shadow-md"
      // Keep focus in the textarea while the list is used with the mouse.
      onMouseDown={(event) => event.preventDefault()}
    >
      <div className="border-b px-3 py-1.5 text-xs text-muted-foreground">
        Attach a resource
      </div>
      {empty ? (
        <div className="px-3 py-3 text-sm text-muted-foreground">{empty}</div>
      ) : loading && results.length === 0 ? (
        <div className="flex justify-center py-3">
          <Spinner />
        </div>
      ) : (
        <ul className="max-h-64 overflow-y-auto py-1" role="listbox">
          {results.map((resource, index) => (
            <li
              key={resource.id}
              role="option"
              aria-selected={index === highlighted}
            >
              <button
                type="button"
                className={cn(
                  "flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
                  index === highlighted && "bg-accent text-accent-foreground"
                )}
                onMouseEnter={() => onHighlight(index)}
                onClick={() => onSelect(resource)}
              >
                <ResourceIcon
                  mediaType={resource.media_type}
                  className="text-muted-foreground"
                />
                <span className="min-w-0 flex-1 truncate">
                  {resource.display_name}
                </span>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {attachedIds.has(resource.id)
                    ? "Attached"
                    : `${formatLabel(resource.media_type)} · ${formatBytes(resource.size)}`}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
