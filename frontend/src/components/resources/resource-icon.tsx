import { HugeiconsIcon } from "@hugeicons/react"
import {
  File02Icon,
  FileAttachmentIcon,
  Pdf01Icon,
  SourceCodeIcon,
} from "@hugeicons/core-free-icons"

import { cn } from "@/lib/utils"

function iconFor(mediaType: string) {
  if (mediaType === "application/pdf") return Pdf01Icon
  if (mediaType === "text/html" || mediaType.includes("json"))
    return SourceCodeIcon
  if (mediaType.startsWith("text/")) return File02Icon
  return FileAttachmentIcon
}

export function ResourceIcon({
  mediaType,
  className,
}: {
  mediaType: string
  className?: string
}) {
  return (
    <HugeiconsIcon
      icon={iconFor(mediaType)}
      strokeWidth={2}
      className={cn("size-4 shrink-0", className)}
    />
  )
}
