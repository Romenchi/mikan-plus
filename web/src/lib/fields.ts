import { ApiError } from "../api/client";

/**
 * The API's field errors of a failed mutation (`{ public_host: "…" }`), empty when it did
 * not fail or failed for a reason that is not about a field (a network error, a 500).
 * Replaces `save.error instanceof ApiError ? save.error.fields : {}` at every form.
 */
export function fieldErrors(error: unknown): Record<string, string> {
  return error instanceof ApiError ? error.fields : {};
}
