import { useEffect, useState } from "react"
import { toast } from "sonner"

import { HugeiconsIcon } from "@hugeicons/react"
import { Download01Icon } from "@hugeicons/core-free-icons"

import { ResourceIcon } from "@/components/resources/resource-icon"
import {
  formatBytes,
  formatLabel,
  getResourceState,
  openResourceFile,
  resourceErrorMessage,
  type Resource,
} from "@/lib/resources"
import { cn } from "@/lib/utils"
import type { ResourceAttachmentData } from "@/types/chat"

/**
 * A resource attached to a sent message. It shows the snapshot taken when the
 * message was sent and only asks the server whether the resource still
 * exists, to offer open and download or mark it unavailable.
 */
function ResourceAttachmentCard({ data }: { data: ResourceAttachmentData }) {
  const [state, setState] = useState<{
    id: string
    resource: Resource | null
  } | null>(null)

  useEffect(() => {
    let cancelled = false
    getResourceState(data.resourceId)
      .then((resource) => {
        if (!cancelled) setState({ id: data.resourceId, resource })
      })
      .catch(() => {
        // Leave the card in its neutral state; the snapshot still renders.
      })
    return () => {
      cancelled = true
    }
  }, [data.resourceId])

  const current = state?.id === data.resourceId ? state.resource : undefined
  const unavailable = current === null
  const name = data.name || current?.display_name || "Attachment"
  const mediaType = data.mediaType || current?.media_type || ""
  const size = data.size ?? current?.size

  const open = (download: boolean) => {
    if (!current) return
    void openResourceFile(current, download).catch((error: unknown) => {
      toast.error(resourceErrorMessage(error, "Could not open the file."))
    })
  }

  return (
    <div
      className={cn(
        "flex w-64 max-w-full min-w-0 items-center gap-2.5 rounded-lg border bg-background/60 px-3 py-2 text-left",
        unavailable && "opacity-60"
      )}
    >
      <button
        type="button"
        className="flex min-w-0 flex-1 items-center gap-2.5 text-left disabled:cursor-default"
        disabled={!current}
        onClick={() => open(false)}
        title={unavailable ? "This resource was deleted" : `Open ${name}`}
      >
        <ResourceIcon
          mediaType={mediaType}
          className="size-5 text-muted-foreground"
        />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{name}</span>
          <span className="block truncate text-xs text-muted-foreground">
            {unavailable
              ? "Unavailable"
              : [
                  formatLabel(mediaType),
                  size !== undefined && formatBytes(size),
                ]
                  .filter(Boolean)
                  .join(" · ")}
          </span>
        </span>
      </button>
      {current && (
        <button
          type="button"
          className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
          onClick={() => open(true)}
          aria-label={`Download ${name}`}
        >
          <HugeiconsIcon
            icon={Download01Icon}
            strokeWidth={2}
            className="size-4"
          />
        </button>
      )}
    </div>
  )
}

export function ResourceAttachmentGroup({
  attachments,
}: {
  attachments: ResourceAttachmentData[]
}) {
  if (attachments.length === 0) return null
  return (
    <div className="flex flex-wrap justify-end gap-2">
      {attachments.map((data) => (
        <ResourceAttachmentCard key={data.resourceId} data={data} />
      ))}
    </div>
  )
}
