/** 바이트 수를 B · KB · MB · GB 로 */
export const formatSize = (size: number) =>
  size < 1024
    ? `${size} B`
    : size < 1024 ** 2
      ? `${(size / 1024).toFixed(1)} KB`
      : size < 1024 ** 3
        ? `${(size / 1024 ** 2).toFixed(1)} MB`
        : `${(size / 1024 ** 3).toFixed(2)} GB`;

const pad = (n: number) => String(n).padStart(2, '0');

/** ms → YYYY-MM-DD HH:mm:ss (24시간제, 이 PC 의 시간대) */
export const formatDateTime = (ms: number) => {
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
};
