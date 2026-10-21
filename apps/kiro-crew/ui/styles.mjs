export const box = {
  border: "1px solid var(--border, #8885)",
  borderRadius: 12,
  padding: 24,
  marginBottom: 24,
};
export const button = {
  padding: "10px 16px",
  lineHeight: 1.4,
  border: "1px solid var(--border, #8886)",
  borderRadius: 8,
  cursor: "pointer",
  background: "transparent",
  color: "inherit",
};
export const selectedButton = {
  ...button,
  background: "#b4a0ff",
  color: "#211b32",
  borderColor: "#b4a0ff",
  fontWeight: 650,
};
export const reactions = [
  ["eyes", "👀 Eyes"],
  ["+1", "👍 Thumbs up"],
  ["-1", "👎 Thumbs down"],
  ["laugh", "😄 Laugh"],
  ["confused", "😕 Confused"],
  ["heart", "❤️ Heart"],
  ["hooray", "🎉 Hooray"],
  ["rocket", "🚀 Rocket"],
];
export const input = {
  display: "block",
  width: "100%",
  background: "transparent",
  color: "inherit",
  border: "1px solid #8886",
  borderRadius: 8,
  padding: 10,
  boxSizing: "border-box",
  margin: "10px 0 20px",
};
