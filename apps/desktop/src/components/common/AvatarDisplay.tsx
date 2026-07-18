interface AvatarDisplayProps {
  /** Diameter in px */
  size: number;
  /** data-URL or null */
  avatarUrl: string | null;
  /** Fallback initial derived from username */
  username?: string;
  className?: string;
}

const PALETTE = [
  "#0284c7", // sky-600
  "#0891b2", // cyan-600
  "#059669", // emerald-600
  "#7c3aed", // violet-600
  "#db2777", // pink-600
  "#d97706", // amber-600
  "#4f46e5", // indigo-600
  "#0d9488", // teal-600
];

function pickColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = name.charCodeAt(i) + ((hash << 5) - hash);
  }
  // PALETTE is a fixed non-empty array; index is always in range.
  return PALETTE[Math.abs(hash) % PALETTE.length]!;
}

export function AvatarDisplay({
  size,
  avatarUrl,
  username = "",
  className = "",
}: AvatarDisplayProps) {
  const initial = username.charAt(0).toUpperCase() || "?";
  const bg = pickColor(username);
  const fontSize = Math.max(Math.round(size * 0.42), 10);

  return (
    <div
      className={`avatar-display shrink-0 ${className}`}
      style={{
        width: size,
        height: size,
        borderRadius: "50%",
        overflow: "hidden",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: avatarUrl ? "transparent" : bg,
        color: "#fff",
        fontSize,
        fontWeight: 600,
        lineHeight: 1,
        userSelect: "none",
      }}
    >
      {avatarUrl ? (
        <img
          src={avatarUrl}
          alt="avatar"
          style={{ width: "100%", height: "100%", objectFit: "cover" }}
          draggable={false}
        />
      ) : (
        <span>{initial}</span>
      )}
    </div>
  );
}
