import * as SwitchPrimitive from "@radix-ui/react-switch";

/** An on/off switch. Kept out of ui.tsx: the subscription page has none and should not carry Radix. */
export function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <SwitchPrimitive.Root className="switch" checked={checked} onCheckedChange={onChange} aria-label={label} disabled={disabled}>
      <SwitchPrimitive.Thumb className="thumb" />
    </SwitchPrimitive.Root>
  );
}
