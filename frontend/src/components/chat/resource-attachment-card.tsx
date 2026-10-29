import { useEffect, useState } from "react"
import { toast } from "sonner"

import { HugeiconsIcon } from "@hugeicons/react"
import { Download01Icon } from "@hugeicons/core-free-icons"

import { ResourceIcon } from "@/components/resources/resource-icon"
import {
  Attachment,
  AttachmentAction,
  AttachmentActions,
  AttachmentContent,
  AttachmentDescription,
  AttachmentGroup,
  AttachmentMedia,
  AttachmentTitle,
  AttachmentTrigger,
} from "@/components/ui/attachment"
import {
  formatBytes,
  formatLabel,
  getResourceState,
  openResourceFile,
  resourceErrorMessage,
  resourceTypeTone,
  type Resource,
} from "@/lib/resources"
import { cn } from "@/lib/utils"
import type { ResourceAttachmentData } from "@/types/chat"

/**
 * A resource attached to a sent message. It shows the snapshot taken when the
 * message was sent and only asks the server whether the resource still
 * exists, to offer open and download or mark it unavailable.
 */
export function ResourceAttachmentCard({
  data,
}: {
  data: ResourceAttachmentData
}) {
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
    <Attachment
      className={cn(
        "max-w-64",
        unavailable && "border-dashed bg-transparent text-muted-foreground"
      )}
    >
      <AttachmentMedia
        className={cn(
          resourceTypeTone(mediaType, name),
          unavailable && "grayscale"
        )}
      >
        <ResourceIcon mediaType={mediaType} />
      </AttachmentMedia>
      <AttachmentContent>
        <AttachmentTitle className={cn(unavailable && "line-through")}>
          {name}
        </AttachmentTitle>
        <AttachmentDescription>
          {unavailable
            ? "Deleted"
            : [formatLabel(mediaType), size !== undefined && formatBytes(size)]
                .filter(Boolean)
                .join(" · ")}
        </AttachmentDescription>
      </AttachmentContent>
      {current && (
        <>
          <AttachmentActions>
            <AttachmentAction
              aria-label={`Download ${name}`}
              title="Download"
              onClick={() => open(true)}
            >
              <HugeiconsIcon icon={Download01Icon} strokeWidth={2} />
            </AttachmentAction>
          </AttachmentActions>
          <AttachmentTrigger
            aria-label={`Open ${name}`}
            title={`Open ${name}`}
            onClick={() => open(false)}
          />
        </>
      )}
    </Attachment>
  )
}

export function ResourceAttachmentGroup({
  attachments,
}: {
  attachments: ResourceAttachmentData[]
}) {
  if (attachments.length === 0) return null
  return (
    <AttachmentGroup
      role="group"
      aria-label="Attached files"
      className="max-w-full self-end"
    >
      {attachments.map((data) => (
        <ResourceAttachmentCard key={data.resourceId} data={data} />
      ))}
    </AttachmentGroup>
  )
}
