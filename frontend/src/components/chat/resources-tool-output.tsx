import { useEffect, useState, type ReactNode } from "react"
import { ClientResponseError } from "pocketbase"
import { toast } from "sonner"

import { HugeiconsIcon, type IconSvgElement } from "@hugeicons/react"
import {
  ChevronDownIcon,
  File02Icon,
  FolderLibraryIcon,
  Link04Icon,
  Search01Icon,
  Unlink04Icon,
} from "@hugeicons/core-free-icons"
import { CheckCircleIcon, ClockIcon, XCircleIcon } from "lucide-react"
import type { DynamicToolUIPart } from "ai"

import { ResourceIcon } from "@/components/resources/resource-icon"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Spinner } from "@/components/ui/spinner"
import { pb } from "@/lib/pocketbase"
import {
  formatBytes,
  formatLabel,
  RESOURCE_LEASES_COLLECTION,
  resourceErrorMessage,
} from "@/lib/resources"
import { cn } from "@/lib/utils"

import { ToolInput } from "./tool-input"

type ResourceRef = { id: string; name: string; mediaType?: string }

type ListOutput = {
  query?: string
  resources: Array<{
    id: string
    name: string
    mediaType: string
    size: number
    matchedIn?: "name" | "content"
  }>
  truncated: boolean
}

type SearchOutput = {
  resource: ResourceRef
  query: string
  matches: Array<{ offset: number; excerpt: string }>
  truncated: boolean
}

type ReadOutput = {
  resource: ResourceRef
  content: string
  offset: number
  contentChars: number
  totalChars: number
  truncated: boolean
}

type LinkOutput = {
  resource: ResourceRef
  leaseId: string
  url: string
  expiresAt: string
}

function parseOutput<T>(output: unknown): T | null {
  if (typeof output === "string") {
    try {
      return JSON.parse(output) as T
    } catch {
      return null
    }
  }
  return output && typeof output === "object" ? (output as T) : null
}

function StatusBadge({ state }: { state: DynamicToolUIPart["state"] }) {
  if (state === "input-streaming" || state === "input-available") {
    return (
      <Badge className="gap-1.5 rounded-full text-xs" variant="secondary">
        <ClockIcon className="size-4 animate-pulse" />
        Running
      </Badge>
    )
  }
  if (state === "output-error") {
    return (
      <Badge className="gap-1.5 rounded-full text-xs" variant="secondary">
        <XCircleIcon className="size-4 text-red-600" />
        Error
      </Badge>
    )
  }
  if (state === "output-available") {
    return (
      <Badge className="gap-1.5 rounded-full text-xs" variant="secondary">
        <CheckCircleIcon className="size-4 text-green-600" />
        Completed
      </Badge>
    )
  }
  return null
}

function ToolShell({
  part,
  icon,
  title,
  count,
  children,
}: {
  part: DynamicToolUIPart
  icon: IconSvgElement
  title: ReactNode
  count?: number
  children?: ReactNode
}) {
  return (
    <Collapsible
      defaultOpen={false}
      className="group not-prose mb-4 w-full rounded-md border"
    >
      <CollapsibleTrigger className="flex w-full items-center justify-between gap-4 p-3">
        <div className="flex min-w-0 items-center gap-2">
          <HugeiconsIcon
            icon={icon}
            strokeWidth={2}
            className="size-4 shrink-0 text-muted-foreground"
          />
          <span className="truncate text-sm font-medium">{title}</span>
          {count !== undefined && count > 0 && (
            <Badge variant="secondary" className="rounded-full px-1.5">
              {count}
            </Badge>
          )}
          <StatusBadge state={part.state} />
        </div>
        <HugeiconsIcon
          icon={ChevronDownIcon}
          strokeWidth={2}
          className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-180"
        />
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-3 p-4 pt-0 outline-none">
        <ToolInput input={part.input} />
        {part.state === "output-error" && (
          <div className="rounded-md bg-destructive/10 px-3 py-2 text-xs break-words text-destructive">
            {part.errorText || "Tool execution failed"}
          </div>
        )}
        {part.state === "output-available" && children}
      </CollapsibleContent>
    </Collapsible>
  )
}

export function ResourcesListToolCard({ part }: { part: DynamicToolUIPart }) {
  const output = parseOutput<ListOutput>(part.output)
  const resources = output?.resources ?? []
  return (
    <ToolShell
      part={part}
      icon={FolderLibraryIcon}
      title="Resources"
      count={resources.length}
    >
      {resources.length === 0 ? (
        <div className="rounded-md bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
          No resources found
        </div>
      ) : (
        <div className="space-y-1.5">
          {resources.map((resource) => (
            <div
              key={resource.id}
              className="flex items-center gap-2 rounded-md border bg-card px-3 py-2"
            >
              <ResourceIcon
                mediaType={resource.mediaType}
                className="text-muted-foreground"
              />
              <span className="min-w-0 flex-1 truncate text-sm font-medium">
                {resource.name}
              </span>
              {resource.matchedIn === "content" && (
                <Badge variant="outline" className="text-[10px]">
                  content match
                </Badge>
              )}
              <span className="shrink-0 text-xs text-muted-foreground">
                {formatLabel(resource.mediaType)} · {formatBytes(resource.size)}
              </span>
            </div>
          ))}
          {output?.truncated && (
            <p className="text-xs text-muted-foreground">More results exist.</p>
          )}
        </div>
      )}
    </ToolShell>
  )
}

export function ResourcesSearchToolCard({ part }: { part: DynamicToolUIPart }) {
  const output = parseOutput<SearchOutput>(part.output)
  const matches = output?.matches ?? []
  return (
    <ToolShell
      part={part}
      icon={Search01Icon}
      title={output ? `Search in ${output.resource.name}` : "Search resource"}
      count={matches.length}
    >
      {matches.length === 0 ? (
        <div className="rounded-md bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
          No matches for “{output?.query}”
        </div>
      ) : (
        <div className="space-y-1.5">
          {matches.map((match) => (
            <div
              key={match.offset}
              className="rounded-md border bg-card px-3 py-2 text-xs"
            >
              <p className="line-clamp-4 whitespace-pre-wrap text-muted-foreground">
                {match.excerpt}
              </p>
              <span className="mt-1 block text-[10px] text-muted-foreground/70 tabular-nums">
                Character {match.offset.toLocaleString()}
              </span>
            </div>
          ))}
          {output?.truncated && (
            <p className="text-xs text-muted-foreground">More matches exist.</p>
          )}
        </div>
      )}
    </ToolShell>
  )
}

const READ_PREVIEW_CHARS = 1200

export function ResourcesReadToolCard({ part }: { part: DynamicToolUIPart }) {
  const output = parseOutput<ReadOutput>(part.output)
  const preview = output?.content.slice(0, READ_PREVIEW_CHARS) ?? ""
  return (
    <ToolShell
      part={part}
      icon={File02Icon}
      title={output ? `Read ${output.resource.name}` : "Read resource"}
    >
      {output && (
        <div className="space-y-2">
          <p className="text-xs text-muted-foreground tabular-nums">
            Characters {(output.offset + 1).toLocaleString()}–
            {(output.offset + output.contentChars).toLocaleString()} of{" "}
            {output.totalChars.toLocaleString()}
            {output.truncated && " · more to read"}
          </p>
          <pre className="max-h-64 overflow-auto rounded-md border bg-muted/30 p-3 text-xs break-words whitespace-pre-wrap">
            {preview}
            {output.content.length > preview.length && "…"}
          </pre>
        </div>
      )}
    </ToolShell>
  )
}

function remaining(expiresAt: number, now: number) {
  const seconds = Math.max(0, Math.round((expiresAt - now) / 1000))
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`
}

/**
 * The result of resources_create_download_url. The URL is a bearer
 * credential persisted with the conversation, so it is never rendered as a
 * link, never copied automatically, and hidden once it has expired or been
 * revoked.
 */
export function ResourceDownloadLinkCard({
  part,
}: {
  part: DynamicToolUIPart
}) {
  const output = parseOutput<LinkOutput>(part.output)
  const link = output?.url && output.leaseId ? output : null
  const expiresAt = link ? new Date(link.expiresAt).getTime() : 0
  const [now, setNow] = useState(() => Date.now())
  const [revoked, setRevoked] = useState<boolean | null>(null)
  const [revoking, setRevoking] = useState(false)
  const expired = !link || now >= expiresAt

  useEffect(() => {
    if (!link || expired) return
    let cancelled = false
    pb.collection(RESOURCE_LEASES_COLLECTION)
      .getOne(link.leaseId, { fields: "id", requestKey: null })
      .then(() => {
        if (!cancelled) setRevoked(false)
      })
      .catch((error: unknown) => {
        if (
          !cancelled &&
          error instanceof ClientResponseError &&
          error.status === 404
        ) {
          setRevoked(true)
        }
      })
    return () => {
      cancelled = true
    }
    // Checked once per mount: a revoke from this card updates state itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [link?.leaseId])

  useEffect(() => {
    if (!link || expired || revoked) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [link, expired, revoked])

  if (!link) {
    // Approval, denial, and pending states are shown by the approval card.
    if (part.state !== "output-error") return null
    return <ToolShell part={part} icon={Link04Icon} title="Download link" />
  }

  const valid = !expired && revoked !== true
  const revoke = async () => {
    setRevoking(true)
    try {
      await pb.send(
        `/api/resource-download-leases/${encodeURIComponent(link.leaseId)}`,
        { method: "DELETE", requestKey: null }
      )
      setRevoked(true)
    } catch (error) {
      toast.error(resourceErrorMessage(error, "Could not revoke the link."))
    } finally {
      setRevoking(false)
    }
  }

  return (
    <Collapsible
      defaultOpen={false}
      className="group not-prose mb-4 w-full rounded-md border"
    >
      <CollapsibleTrigger className="flex w-full items-center justify-between gap-4 p-3">
        <div className="flex min-w-0 items-center gap-2">
          <HugeiconsIcon
            icon={valid ? Link04Icon : Unlink04Icon}
            strokeWidth={2}
            className="size-4 shrink-0 text-muted-foreground"
          />
          <span className="truncate text-sm font-medium">
            Download link · {link.resource.name}
          </span>
          <Badge
            variant={valid ? "secondary" : "outline"}
            className="rounded-full text-xs tabular-nums"
          >
            {valid
              ? `Expires in ${remaining(expiresAt, now)}`
              : revoked
                ? "Revoked"
                : "Expired"}
          </Badge>
        </div>
        <HugeiconsIcon
          icon={ChevronDownIcon}
          strokeWidth={2}
          className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-180"
        />
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-3 p-4 pt-0 outline-none">
        {valid ? (
          <>
            <p className="text-xs text-muted-foreground">
              Anyone with this URL can download the file without signing in
              until it expires.
            </p>
            <code
              className={cn(
                "block rounded-md border bg-muted/30 px-3 py-2 font-mono text-xs break-all select-all"
              )}
            >
              {link.url}
            </code>
            <div className="flex justify-end">
              <Button
                variant="outline"
                size="sm"
                disabled={revoking}
                onClick={() => void revoke()}
              >
                {revoking && <Spinner />}
                Revoke now
              </Button>
            </div>
          </>
        ) : (
          <p className="text-xs text-muted-foreground">
            This link no longer works. Ask for a new one, or check the Resources
            page.
          </p>
        )}
      </CollapsibleContent>
    </Collapsible>
  )
}

export function ResourcesRevokeToolCard({ part }: { part: DynamicToolUIPart }) {
  return (
    <ToolShell part={part} icon={Unlink04Icon} title="Revoke download link">
      <div className="rounded-md bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
        The download link no longer works.
      </div>
    </ToolShell>
  )
}
