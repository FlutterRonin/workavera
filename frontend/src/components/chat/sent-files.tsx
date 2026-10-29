import { useEffect, useState } from "react"
import { Link } from "react-router"
import { toast } from "sonner"
import type { DynamicToolUIPart } from "ai"

import { HugeiconsIcon } from "@hugeicons/react"
import {
  Download01Icon,
  File02Icon,
  SourceCodeIcon,
} from "@hugeicons/core-free-icons"

import { ResourceAttachmentCard } from "@/components/chat/resource-attachment-card"
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
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { pb } from "@/lib/pocketbase"
import {
  formatBytes,
  formatLabel,
  getResourceState,
  openResourceFile,
  RESOURCES_COLLECTION,
  resourceErrorMessage,
  type Resource,
} from "@/lib/resources"
import { workspaceRecordUrl } from "@/lib/workspace-navigation"

/** Matches the thumbnail size configured on user_resources.file. */
const THUMB_SIZE = "480x0"

type SentFile =
  | {
      kind: "resource"
      resourceId: string
      name: string
      mediaType: string
      size: number
    }
  | { kind: "doc"; docId: string; name: string; docKind: string }

function parseFiles(output: unknown): SentFile[] {
  let value = output
  if (typeof value === "string") {
    try {
      value = JSON.parse(value)
    } catch {
      return []
    }
  }
  const files = (value as { files?: unknown } | null)?.files
  return Array.isArray(files) ? (files as SentFile[]) : []
}

/** Builds a URL for a protected resource file with a fresh file token. */
async function protectedFileUrl(resource: Resource, thumb?: string) {
  const token = await pb.files.getToken({ requestKey: null })
  return pb.files.getURL(
    { id: resource.id, collectionName: RESOURCES_COLLECTION },
    resource.file,
    { token, ...(thumb ? { thumb } : {}) }
  )
}

/**
 * The files the assistant sent with chat_send_files, shown as cards: images
 * with a thumbnail that enlarges on click, other resources as attachment
 * cards, and documents linking to Docs.
 */
export function SentFiles({ part }: { part: DynamicToolUIPart }) {
  const files = parseFiles(part.output)
  if (files.length === 0) return null
  return (
    <AttachmentGroup
      role="group"
      aria-label="Files from the assistant"
      className="not-prose mb-4 max-w-full items-start"
    >
      {files.map((file) =>
        file.kind === "doc" ? (
          <SentDocCard key={`doc:${file.docId}`} file={file} />
        ) : file.mediaType.startsWith("image/") ? (
          <SentImageCard key={`resource:${file.resourceId}`} file={file} />
        ) : (
          <ResourceAttachmentCard
            key={`resource:${file.resourceId}`}
            data={{
              resourceId: file.resourceId,
              name: file.name,
              mediaType: file.mediaType,
              size: file.size,
            }}
          />
        )
      )}
    </AttachmentGroup>
  )
}

function SentImageCard({
  file,
}: {
  file: Extract<SentFile, { kind: "resource" }>
}) {
  const [resource, setResource] = useState<Resource | null | undefined>()
  const [thumbUrl, setThumbUrl] = useState<string | null>(null)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getResourceState(file.resourceId)
      .then(async (current) => {
        if (cancelled) return
        setResource(current)
        if (current) {
          const url = await protectedFileUrl(current, THUMB_SIZE)
          if (!cancelled) setThumbUrl(url)
        }
      })
      .catch(() => {
        // Keep the neutral card; the stored name and size still render.
      })
    return () => {
      cancelled = true
    }
  }, [file.resourceId])

  const deleted = resource === null
  const fail = (error: unknown) =>
    toast.error(resourceErrorMessage(error, "Could not open the image."))

  const preview = () => {
    if (!resource) return
    protectedFileUrl(resource).then(setPreviewUrl).catch(fail)
  }

  const download = () => {
    if (!resource) return
    void openResourceFile(resource, true).catch(fail)
  }

  const description = deleted
    ? "Deleted"
    : `${formatLabel(file.mediaType)} · ${formatBytes(file.size)}`

  return (
    <>
      <Attachment
        orientation="vertical"
        className="w-44"
        state={deleted ? "idle" : "done"}
      >
        <AttachmentMedia variant="image">
          {thumbUrl ? (
            <img src={thumbUrl} alt={file.name} loading="lazy" />
          ) : deleted ? (
            <ResourceIcon mediaType={file.mediaType} />
          ) : (
            <Skeleton className="size-full rounded-none" />
          )}
        </AttachmentMedia>
        <AttachmentContent>
          <AttachmentTitle>{file.name}</AttachmentTitle>
          <AttachmentDescription>{description}</AttachmentDescription>
        </AttachmentContent>
        {resource && (
          <>
            <AttachmentActions>
              <AttachmentAction
                variant="secondary"
                aria-label={`Download ${file.name}`}
                title="Download"
                onClick={download}
              >
                <HugeiconsIcon icon={Download01Icon} strokeWidth={2} />
              </AttachmentAction>
            </AttachmentActions>
            <AttachmentTrigger
              aria-label={`Preview ${file.name}`}
              title={`Preview ${file.name}`}
              onClick={preview}
            />
          </>
        )}
      </Attachment>

      <Dialog
        open={previewUrl !== null}
        onOpenChange={(open) => {
          if (!open) setPreviewUrl(null)
        }}
      >
        <DialogContent className="sm:max-w-[min(90vw,72rem)]">
          <DialogHeader>
            <DialogTitle className="truncate pr-8">{file.name}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          {previewUrl && (
            <img
              src={previewUrl}
              alt={file.name}
              className="mx-auto max-h-[70vh] w-auto max-w-full rounded-lg object-contain"
            />
          )}
          <DialogFooter>
            <Button variant="outline" onClick={download}>
              <HugeiconsIcon icon={Download01Icon} strokeWidth={2} />
              Download
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

function SentDocCard({ file }: { file: Extract<SentFile, { kind: "doc" }> }) {
  return (
    <Attachment className="max-w-64">
      <AttachmentMedia>
        <HugeiconsIcon
          icon={file.docKind === "html" ? SourceCodeIcon : File02Icon}
          strokeWidth={2}
        />
      </AttachmentMedia>
      <AttachmentContent>
        <AttachmentTitle>{file.name}</AttachmentTitle>
        <AttachmentDescription>
          {file.docKind === "html" ? "HTML document" : "Markdown document"}
        </AttachmentDescription>
      </AttachmentContent>
      <AttachmentTrigger asChild>
        <Link
          to={workspaceRecordUrl("docs", file.docId)}
          aria-label={`Open ${file.name}`}
          title={`Open ${file.name}`}
        />
      </AttachmentTrigger>
    </Attachment>
  )
}
