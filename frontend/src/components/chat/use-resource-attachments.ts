import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import { isRequestAbort } from "@/lib/error"
import { pb } from "@/lib/pocketbase"
import {
  MAX_MESSAGE_RESOURCES,
  RESOURCE_LIST_FIELDS,
  RESOURCES_COLLECTION,
  resourceErrorMessage,
  uploadResource,
  useResourceMaxSize,
  validateResourceFile,
  type Resource,
} from "@/lib/resources"
import { resourceNameFilter } from "@/store/resources"

export type InputAttachment = {
  key: string
  status: "uploading" | "ready" | "error"
  name: string
  mediaType: string
  size: number
  resourceId?: string
  error?: string
  file?: File
}

function readyAttachment(resource: Resource): InputAttachment {
  return {
    key: resource.id,
    status: "ready",
    name: resource.display_name,
    mediaType: resource.media_type,
    size: resource.size,
    resourceId: resource.id,
  }
}

/**
 * The resources a message under composition references. Files are uploaded as
 * soon as they are added, so a message only ever references resources that
 * already exist; removing a chip drops the reference, never the resource.
 */
export function useResourceAttachments() {
  const [attachments, setAttachments] = useState<InputAttachment[]>([])
  const maxSize = useResourceMaxSize()

  const full = attachments.length >= MAX_MESSAGE_RESOURCES
  const uploading = attachments.some((item) => item.status === "uploading")
  const ready = attachments.filter((item) => item.status === "ready")

  const runUpload = useCallback(async (key: string, file: File) => {
    try {
      const resource = await uploadResource(file)
      setAttachments((items) => {
        // An identical file already attached resolves to the same resource.
        if (items.some((item) => item.resourceId === resource.id)) {
          return items.filter((item) => item.key !== key)
        }
        return items.map((item) =>
          item.key === key ? { ...readyAttachment(resource), key } : item
        )
      })
    } catch (error) {
      setAttachments((items) =>
        items.map((item) =>
          item.key === key
            ? {
                ...item,
                status: "error",
                error: resourceErrorMessage(error, "Upload failed."),
              }
            : item
        )
      )
    }
  }, [])

  const addFiles = useCallback(
    (files: File[]) => {
      if (files.length === 0) return
      if (files.length > 1) {
        toast.info("Only the first file was added. Add files one at a time.")
      }
      const file = files[0]
      if (full) {
        toast.error(
          `A message can attach at most ${MAX_MESSAGE_RESOURCES} files.`
        )
        return
      }
      const invalid = validateResourceFile(file, maxSize)
      if (invalid) {
        toast.error(`${file.name}: ${invalid}`)
        return
      }
      const key = crypto.randomUUID()
      setAttachments((items) => [
        ...items,
        {
          key,
          status: "uploading",
          name: file.name,
          mediaType: file.type,
          size: file.size,
          file,
        },
      ])
      void runUpload(key, file)
    },
    [full, maxSize, runUpload]
  )

  const addResource = useCallback((resource: Resource) => {
    setAttachments((items) => {
      if (
        items.length >= MAX_MESSAGE_RESOURCES ||
        items.some((item) => item.resourceId === resource.id)
      ) {
        return items
      }
      return [...items, readyAttachment(resource)]
    })
  }, [])

  const retry = useCallback(
    (key: string) => {
      const attachment = attachments.find((item) => item.key === key)
      if (!attachment?.file) return
      setAttachments((items) =>
        items.map((item) =>
          item.key === key
            ? { ...item, status: "uploading", error: undefined }
            : item
        )
      )
      void runUpload(key, attachment.file)
    },
    [attachments, runUpload]
  )

  const remove = useCallback((key: string) => {
    setAttachments((items) => items.filter((item) => item.key !== key))
  }, [])

  const clear = useCallback(() => setAttachments([]), [])

  return {
    attachments,
    ready,
    full,
    uploading,
    addFiles,
    addResource,
    retry,
    remove,
    clear,
  }
}

/** The `#` token being typed right before the caret, if any. */
export type ResourceTrigger = { start: number; end: number; query: string }

const triggerPattern = /(?:^|\s)#([^\s#]{0,64})$/

export function findResourceTrigger(
  value: string,
  caret: number
): ResourceTrigger | null {
  const match = triggerPattern.exec(value.slice(0, caret))
  if (!match) return null
  return { start: caret - match[1].length - 1, end: caret, query: match[1] }
}

/**
 * Lists the user's resources while a `#` token is being typed. Navigation
 * keys are handled by the textarea through `onKeyDown`, which this hook
 * returns, so focus never leaves the input.
 */
export function useResourcePicker({
  trigger,
  disabled,
  onSelect,
  onDismiss,
}: {
  trigger: ResourceTrigger | null
  /** True when no more resources can be attached; only Escape works. */
  disabled: boolean
  onSelect: (resource: Resource) => void
  onDismiss: () => void
}) {
  const [results, setResults] = useState<Resource[]>([])
  const [loading, setLoading] = useState(false)
  const [highlighted, setHighlighted] = useState(0)
  const query = trigger?.query ?? null
  const latestQuery = useRef(query)

  useEffect(() => {
    latestQuery.current = query
    if (query === null) return
    const timer = window.setTimeout(() => {
      setLoading(true)
      pb.collection(RESOURCES_COLLECTION)
        .getList<Resource>(1, 8, {
          filter: resourceNameFilter(query),
          sort: "-updated",
          fields: RESOURCE_LIST_FIELDS,
          requestKey: "chat-resource-picker",
        })
        .then((page) => {
          if (latestQuery.current !== query) return
          setResults(page.items)
          setHighlighted(0)
          setLoading(false)
        })
        .catch((error: unknown) => {
          if (isRequestAbort(error)) return
          setResults([])
          setLoading(false)
        })
    }, 120)
    return () => window.clearTimeout(timer)
  }, [query])

  const onKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (!trigger) return
    if (event.key === "Escape") {
      event.preventDefault()
      onDismiss()
      return
    }
    if (disabled || results.length === 0) return
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault()
      const step = event.key === "ArrowDown" ? 1 : -1
      setHighlighted(
        (index) => (index + step + results.length) % results.length
      )
      return
    }
    if (
      (event.key === "Enter" || event.key === "Tab") &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault()
      onSelect(results[Math.min(highlighted, results.length - 1)])
    }
  }

  return {
    open: trigger !== null,
    results,
    loading,
    highlighted,
    setHighlighted,
    onKeyDown,
  }
}
