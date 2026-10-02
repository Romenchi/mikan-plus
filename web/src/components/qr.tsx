import { useMemo } from "react";
import { renderSVG } from "uqr";
import { t } from "../i18n";

/**
 * The QR code of a link, drawn by uqr (no network, no images). Loaded on demand through
 * `QR` in ui.tsx: the page does not carry the encoder until a code is on screen.
 */
export default function QRCode({ value, size = 136, label }: { value: string; size?: number; label?: string }) {
  const svg = useMemo(() => renderSVG(value, { ecc: "M", border: 1, blackColor: "#161A24", whiteColor: "#FFFFFF" }), [value]);
  // The svg is made by uqr from the value alone: it holds no markup of anyone else's.
  return <div className="qr" style={{ width: size, height: size }} role="img" aria-label={label ?? t("common.qrLabel")} dangerouslySetInnerHTML={{ __html: svg }} />;
}
