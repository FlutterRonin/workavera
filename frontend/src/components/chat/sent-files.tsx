import { useEffect, useState, type ReactNode } from "react"
import { Link } from "react-router"
import { toast } from "sonner"
import type { DynamicToolUIPart } from "ai"

import { HugeiconsIcon } from "@hugeicons/react"
import {
  Download01Icon,
  File02Icon,
  SourceCodeIcon,
} from "@hugeicons/core-free-icons"

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
import { Badge } from "@/components/ui/badge"
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
  fileExtension,
  formatBytes,
  formatLabel,
  getResourceState,
  openResourceFile,
  RESOURCES_COLLECTION,
  resourceErrorMessage,
  resourceTypeTone,
  type Resource,
} from "@/lib/resources"
import { cn } from "@/lib/utils"
import { workspaceRecordUrl } from "@/lib/workspace-navigation"

/** Matches the thumbnail size configured on user_resources.file. */
const THUMB_SIZE = "480x0"

type SentResource = {
  kind: "resource"
  resourceId: string
  name: string
  mediaType: string
  size: number
}

type SentDoc = { kind: "doc"; docId: string; name: string; docKind: string }

type SentFile = SentResource | SentDoc

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
 * The files the assistant sent with chat_send_files. Every file is the same
 * card: images show their thumbnail and enlarge on click, other files show a
 * preview tile in their type's color, and documents link to Docs.
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
        ) : (
          <SentResourceCard key={`resource:${file.resourceId}`} file={file} />
        )
      )}
    </AttachmentGroup>
  )
}

/** A vertical card of fixed width, so every sent file has the same size. */
function FileCard({
  media,
  imageMedia = false,
  title,
  description,
  unavailable = false,
  children,
}: {
  media: ReactNode
  imageMedia?: boolean
  title: string
  description: string
  unavailable?: boolean
  children?: ReactNode
}) {
  return (
    <Attachment
      orientation="vertical"
      className="has-data-[slot=attachment-content]:w-40"
      state={unavailable ? "idle" : "done"}
    >
      <AttachmentMedia
        variant={imageMedia ? "image" : "icon"}
        className={cn(unavailable && "grayscale")}
      >
        {media}
      </AttachmentMedia>
      <AttachmentContent>
        <AttachmentTitle
          title={title}
          className={cn(unavailable && "line-through")}
        >
          {title}
        </AttachmentTitle>
        <AttachmentDescription>{description}</AttachmentDescription>
      </AttachmentContent>
      {children}
    </Attachment>
  )
}

/**
 * The preview of a file without a thumbnail: its type's icon on a tile in
 * the type's color, labeled with the extension.
 */
function FileTile({
  tone,
  icon,
  label,
}: {
  tone: string
  icon: ReactNode
  label: string
}) {
  return (
    <div
      className={cn(
        "relative flex size-full flex-col items-center justify-center gap-3",
        tone
      )}
    >
      <div
        aria-hidden
        className="absolute inset-0 bg-[radial-gradient(circle_at_30%_20%,color-mix(in_oklab,currentColor_14%,transparent),transparent_60%)]"
      />
      <div className="relative flex size-16 items-center justify-center rounded-2xl bg-background/80 shadow-sm ring-1 ring-current/10 [&_svg]:size-8!">
        {icon}
      </div>
      {label && (
        <Badge
          variant="outline"
          className="relative border-current/20 bg-background/70 font-mono tracking-wider text-current uppercase"
        >
          {label}
        </Badge>
      )}
    </div>
  )
}

function SentResourceCard({ file }: { file: SentResource }) {
  const isImage = file.mediaType.startsWith("image/")
  const [resource, setResource] = useState<Resource | null | undefined>()
  const [thumbUrl, setThumbUrl] = useState<string | null>(null)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getResourceState(file.resourceId)
      .then(async (current) => {
        if (cancelled) return
        setResource(current)
        if (current && isImage) {
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
  }, [file.resourceId, isImage])

  const unavailable = resource === null
  const fail = (error: unknown) =>
    toast.error(resourceErrorMessage(error, "Could not open the file."))

  const open = () => {
    if (!resource) return
    if (isImage) {
      protectedFileUrl(resource).then(setPreviewUrl).catch(fail)
    } else {
      void openResourceFile(resource).catch(fail)
    }
  }

  const download = () => {
    if (!resource) return
    void openResourceFile(resource, true).catch(fail)
  }

  const description = unavailable
    ? "Deleted"
    : `${formatLabel(file.mediaType)} · ${formatBytes(file.size)}`

  const media =
    isImage && thumbUrl ? (
      <img src={thumbUrl} alt={file.name} loading="lazy" />
    ) : isImage && !unavailable ? (
      <Skeleton className="size-full rounded-none" />
    ) : (
      <FileTile
        tone={resourceTypeTone(file.mediaType, file.name)}
        icon={<ResourceIcon mediaType={file.mediaType} />}
        label={fileExtension(file.name)}
      />
    )

  return (
    <>
      <FileCard
        media={media}
        imageMedia={isImage && !!thumbUrl}
        title={file.name}
        description={description}
        unavailable={unavailable}
      >
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
              aria-label={`Open ${file.name}`}
              title={`Open ${file.name}`}
              onClick={open}
            />
          </>
        )}
      </FileCard>

      {isImage && (
        <Dialog
          open={previewUrl !== null}
          onOpenChange={(isOpen) => {
            if (!isOpen) setPreviewUrl(null)
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
      )}
    </>
  )
}

function SentDocCard({ file }: { file: SentDoc }) {
  const html = file.docKind === "html"
  return (
    <FileCard
      media={
        <FileTile
          tone={resourceTypeTone(html ? "text/html" : "text/markdown", "")}
          icon={
            <HugeiconsIcon
              icon={html ? SourceCodeIcon : File02Icon}
              strokeWidth={2}
            />
          }
          label={html ? "html" : "md"}
        />
      }
      title={file.name}
      description={html ? "HTML document" : "Markdown document"}
    >
      <AttachmentTrigger asChild>
        <Link
          to={workspaceRecordUrl("docs", file.docId)}
          aria-label={`Open ${file.name}`}
          title={`Open ${file.name}`}
        />
      </AttachmentTrigger>
    </FileCard>
  )
}
