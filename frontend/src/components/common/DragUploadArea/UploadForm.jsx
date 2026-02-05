import React, { useCallback, useRef } from "react";

export default function UploadForm({
  // 新接口：渲染 N 个槽位
  slots = 2,
  files = [],
  accept = ".xlsx,.xls",
  disabled = false,
  onPickAt,
  onClearAt,
  // 兼容旧接口（无调用则忽略）：
  file1,
  file2,
  onPickFile1,
  onPickFile2,
  onClear1,
  onClear2,
}) {
  const inputRefs = useRef([]);

  const triggerPickAt = useCallback((idx) => {
    if (disabled) return;
    inputRefs.current[idx]?.click?.();
  }, [disabled]);

  const handleChangeAt = useCallback((idx, e) => {
    const f = e.target.files?.[0] || null;
    if (typeof onPickAt === "function") onPickAt(idx, f);
    else {
      // 旧接口回退
      if (idx === 0 && typeof onPickFile1 === "function") onPickFile1(f);
      if (idx === 1 && typeof onPickFile2 === "function") onPickFile2(f);
    }
  }, [onPickAt, onPickFile1, onPickFile2]);

  return (
    <div>
      <div style={{ display: "flex", gap: 12, flexWrap: "wrap" }}>
        {Array.from({ length: slots }).map((_, idx) => {
          const f = Array.isArray(files) ? files[idx] : (idx === 0 ? file1 : idx === 1 ? file2 : null);
          return (
            <div key={idx} style={{ flex: 1, minWidth: 260 }}>
              <div style={{ fontSize: 12, color: "#6b7280", marginBottom: 6 }}>文件 {idx + 1}</div>
              <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <button type="button" onClick={() => triggerPickAt(idx)} disabled={disabled}>
                  {f ? "更换文件" : "选择文件"}
                </button>
                {f && (
                  <>
                    <span style={{ color: "#374151" }}>{f.name}</span>
                    <button type="button" onClick={() => (typeof onClearAt === "function" ? onClearAt(idx) : (idx === 0 ? onClear1?.() : idx === 1 ? onClear2?.() : undefined))} disabled={disabled}>清空</button>
                  </>
                )}
                <input
                  ref={el => (inputRefs.current[idx] = el)}
                  type="file"
                  accept={accept}
                  style={{ display: "none" }}
                  onChange={(e) => handleChangeAt(idx, e)}
                />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}