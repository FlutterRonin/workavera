import { create } from "zustand"

import { isRequestAbort } from "@/lib/error"
import { pb } from "@/lib/pocketbase"
import {
  forgetResourceState,
  RESOURCE_LEASES_COLLECTION,
  RESOURCE_LIST_FIELDS,
  RESOURCES_COLLECTION,
  resourceErrorMessage,
  uploadResource,
  type Resource,
  type ResourceLease,
} from "@/lib/resources"

type ResourcesState = {
  resources: Resource[]
  query: string
  loading: boolean
  error: string | null
  fetchResources: (query?: string) => Promise<void>
  upload: (file: File) => Promise<Resource>
  rename: (id: string, displayName: string) => Promise<void>
  remove: (id: string) => Promise<void>
  fetchMarkdown: (id: string) => Promise<string>
  fetchLeases: (resourceId: string) => Promise<ResourceLease[]>
  revokeLease: (leaseId: string) => Promise<void>
}

/** PocketBase filter for resources whose display name contains the query. */
export function resourceNameFilter(query: string) {
  const trimmed = query.trim()
  return trimmed ? pb.filter("display_name ~ {:query}", { query: trimmed }) : ""
}

export const useResourcesStore = create<ResourcesState>((set, get) => ({
  resources: [],
  query: "",
  loading: false,
  error: null,

  fetchResources: async (query = get().query) => {
    set({ loading: true, error: null, query })
    try {
      const resources = await pb
        .collection(RESOURCES_COLLECTION)
        .getFullList<Resource>({
          filter: resourceNameFilter(query),
          sort: "-updated",
          fields: RESOURCE_LIST_FIELDS,
          requestKey: "resources-list",
        })
      set({ resources, loading: false })
    } catch (error) {
      if (isRequestAbort(error)) return
      set({
        loading: false,
        error: resourceErrorMessage(error, "Could not load resources."),
      })
    }
  },

  upload: async (file) => {
    const resource = await uploadResource(file)
    set((state) => ({
      resources: [
        resource,
        ...state.resources.filter((item) => item.id !== resource.id),
      ],
    }))
    return resource
  },

  rename: async (id, displayName) => {
    const updated = await pb
      .collection(RESOURCES_COLLECTION)
      .update<Resource>(
        id,
        { display_name: displayName },
        { fields: RESOURCE_LIST_FIELDS }
      )
    forgetResourceState(id)
    set((state) => ({
      resources: state.resources.map((item) =>
        item.id === id ? updated : item
      ),
    }))
  },

  remove: async (id) => {
    await pb.send(`/api/resources/${encodeURIComponent(id)}`, {
      method: "DELETE",
      requestKey: null,
    })
    forgetResourceState(id)
    set((state) => ({
      resources: state.resources.filter((item) => item.id !== id),
    }))
  },

  fetchMarkdown: async (id) => {
    const record = await pb
      .collection(RESOURCES_COLLECTION)
      .getOne<{ converted_markdown: string }>(id, {
        fields: "converted_markdown",
        requestKey: null,
      })
    return record.converted_markdown
  },

  fetchLeases: async (resourceId) =>
    pb.collection(RESOURCE_LEASES_COLLECTION).getFullList<ResourceLease>({
      filter: pb.filter("resource = {:resource} && expires_at > @now", {
        resource: resourceId,
      }),
      sort: "expires_at",
      fields: "id,resource,purpose,expires_at,created",
      requestKey: null,
    }),

  revokeLease: async (leaseId) => {
    await pb.send(
      `/api/resource-download-leases/${encodeURIComponent(leaseId)}`,
      { method: "DELETE", requestKey: null }
    )
  },
}))
