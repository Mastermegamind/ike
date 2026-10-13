/** The Ike mark: a uli-style spiral inside a rounded square. */
export function Logo({ size = 30, withWord = true }: { size?: number; withWord?: boolean }) {
  return (
    <span className="brand" aria-label="Ike">
      <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden>
        <rect width="32" height="32" rx="9" fill="var(--accent)" />
        <path
          d="M16 16.2c0-1.3 1.6-1.7 2.3-.7.9 1.4-.4 3.2-2.1 3.3-2.6.1-4-2.7-3.1-4.9 1.3-3.2 5.5-3.8 7.8-1.6 3 2.9 2 8-1.7 9.6-4.4 1.9-9.4-.9-10.3-5.5"
          fill="none"
          stroke="var(--accent-ink)"
          strokeWidth="2.1"
          strokeLinecap="round"
        />
        <circle cx="8.6" cy="9" r="1.5" fill="var(--accent-ink)" />
      </svg>
      {withWord ? <span className="brand-word">ike</span> : null}
    </span>
  );
}

/**
 * Login artwork: flowing parallel lines and dot rows, after the uli body and
 * wall painting of Igbo women. Drawn as SVG so it follows the theme's accent.
 */
export function UliPattern() {
  const waves = Array.from({ length: 9 }, (_, i) => {
    const y = 60 + i * 26;
    return `M-20 ${y} C 120 ${y - 70}, 220 ${y + 90}, 360 ${y + 10} S 560 ${y - 80}, 720 ${y + 30}`;
  });
  const dots = Array.from({ length: 18 }, (_, i) => ({ cx: 40 + i * 38, cy: 470 + Math.sin(i / 2) * 18 }));
  return (
    <svg className="pattern" viewBox="0 0 700 620" preserveAspectRatio="xMidYMid slice" aria-hidden>
      <g fill="none" stroke="currentColor" strokeLinecap="round">
        {waves.map((d, i) => (
          <path key={i} d={d} strokeWidth={i % 3 === 1 ? 3 : 1.2} opacity={i % 3 === 1 ? 1 : 0.55} />
        ))}
        <path d="M520 360c0-22 26-28 38-12 14 20-6 48-34 48-42 0-62-46-46-80 22-50 90-56 124-20" strokeWidth="2.4" />
        <path d="M120 420c40-36 80 36 120 0s80 36 120 0 80 36 120 0" strokeWidth="1.4" opacity="0.7" />
      </g>
      <g fill="currentColor">
        {dots.map((d, i) => <circle key={i} cx={d.cx} cy={d.cy} r={i % 4 === 0 ? 3.4 : 2} opacity="0.8" />)}
      </g>
    </svg>
  );
}
