import { useCallback } from "react";
import { useToast } from "../components/toast";
import { t } from "../i18n";

/**
 * Copies a text to the clipboard and tells the user the result: `done` when it worked, the
 * browser's refusal otherwise (it happens over plain http and in some web views). One
 * place for what used to be eight copies of the same try/catch.
 */
export function useCopy() {
  const toast = useToast();
  return useCallback(
    async (text: string, done: string): Promise<boolean> => {
      try {
        await navigator.clipboard.writeText(text);
        toast.ok(done);
        return true;
      } catch {
        toast.error(t("common.copyFailed"));
        return false;
      }
    },
    [toast],
  );
}
