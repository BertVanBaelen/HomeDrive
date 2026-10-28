// House outline with a drive and a green LED, from the design handoff.
export function Logo({ size = 30, label }: { size?: number; label?: string }) {
  return (
    <svg
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M2.5 11.5L12 3.8l9.5 7.7" />
      <path d="M5.5 9.6V19a1.5 1.5 0 001.5 1.5h10a1.5 1.5 0 001.5-1.5V9.6" />
      <rect x="8.25" y="11.75" width="7.5" height="5.5" rx="1.25" />
      <path d="M10.25 14.5h1.5" />
      <circle cx="13.75" cy="14.5" r="0.9" fill="#4ade80" stroke="none" />
    </svg>
  )
}
