import clsx from "clsx";

/**
 * The backdrop behind the glass, "dawn": warm light from the left — mandarin, rose,
 * honey — and cool from the right — lilac, sky. The two drift apart slowly; paper grain
 * on top. `calm` keeps it still (the subscription page, see .atmo.calm in app.css).
 */
export function Atmosphere({ calm }: { calm?: boolean }) {
  return (
    <div className={clsx("atmo", calm && "calm")} aria-hidden>
      <span className="glow glow-warm" />
      <span className="glow glow-cool" />
      <span className="grain" />
    </div>
  );
}

export function Logo({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden>
      <defs>
        <linearGradient id="cat-grad" x1="4" y1="2" x2="28" y2="30" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="var(--mikan-400, #f7964f)" />
          <stop offset="100%" stopColor="var(--mikan-600, #db6317)" />
        </linearGradient>
      </defs>
      {/* Outer Head & Ears with accent gradient */}
      <path
        d="M9 11.5L5.5 3.5C5.2 2.8 6.1 2.2 6.7 2.7L12 6.5C13.2 6.2 14.6 6 16 6C17.4 6 18.8 6.2 20 6.5L25.3 2.7C25.9 2.2 26.8 2.8 26.5 3.5L23 11.5C25.5 13.8 27 17 27 20.5C27 26.5 22.1 30 16 30C9.9 30 5 26.5 5 20.5C5 17 6.5 13.8 9 11.5Z"
        fill="url(#cat-grad)"
      />
      {/* Inner Ear Details */}
      <path d="M8 8L6.5 4.5L10.5 7.5" stroke="rgba(255,255,255,0.75)" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M24 8L25.5 4.5L21.5 7.5" stroke="rgba(255,255,255,0.75)" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" />
      {/* Happy Closed Eyes */}
      <path d="M10.5 16C11.5 14.8 13.5 14.8 14.5 16" stroke="#ffffff" strokeWidth="1.6" strokeLinecap="round" />
      <path d="M17.5 16C18.5 14.8 20.5 14.8 21.5 16" stroke="#ffffff" strokeWidth="1.6" strokeLinecap="round" />
      {/* Nose */}
      <path d="M15 19.5L17 19.5L16 20.8Z" fill="#ffffff" />
      {/* Cute Mouth */}
      <path d="M16 20.8C15 22.2 13.5 22.2 12.5 21.5" stroke="#ffffff" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M16 20.8C17 22.2 18.5 22.2 19.5 21.5" stroke="#ffffff" strokeWidth="1.4" strokeLinecap="round" />
      {/* Whiskers */}
      <path d="M7 18L11 19" stroke="#ffffff" strokeWidth="1.2" strokeLinecap="round" opacity="0.9" />
      <path d="M6.5 21L11 21.5" stroke="#ffffff" strokeWidth="1.2" strokeLinecap="round" opacity="0.9" />
      <path d="M25 18L21 19" stroke="#ffffff" strokeWidth="1.2" strokeLinecap="round" opacity="0.9" />
      <path d="M25.5 21L21 21.5" stroke="#ffffff" strokeWidth="1.2" strokeLinecap="round" opacity="0.9" />
    </svg>
  );
}
