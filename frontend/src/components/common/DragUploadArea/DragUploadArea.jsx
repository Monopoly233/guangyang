import React, { useCallback, useMemo, useState } from "react";
import UploadForm from "./UploadForm.jsx";

function isAcceptedExcel(file, accept) {
  if (!file) return false;
  const name = file.name?.toLowerCase?.() || "";
  const mime = file.type || "";
  const acceptList = (accept || ".xlsx,.xls").split(",").map(s => s.trim().toLowerCase());
  const byExt = acceptList.some(suffix => suffix.startsWith(".") && name.endsWith(suffix));
  const byMime = acceptList.some(a => !a.startsWith(".") && mime.toLowerCase().includes(a));
  return byExt || byMime;
}

export default function DragUploadArea({
  // 新接口：可配置槽位数量与文件数组
  slots = 2,
  files,
  // 兼容旧接口：file1 / file2 和 onFilesChange(f1,f2)
  file1,
  file2,
  onFilesChange,
  accept = ".xlsx,.xls",
  disabled = false,
  style,
  className,
}) {
  const [isDragging, setIsDragging] = useState(false);

  // 计算当前有效文件数组（优先使用新接口 files，否则从旧接口 file1/file2 推导）
  const effectiveFiles = useMemo(() => {
    let arr;
    if (Array.isArray(files)) {
      arr = files.slice(0, Math.max(0, slots | 0));
    } else {
      arr = [file1 || null, file2 || null];
    }
    while (arr.length < (slots | 0)) arr.push(null);
    return arr;
  }, [files, slots, file1, file2]);

  const emitChange = useCallback((nextArr) => {
    // 如果父组件传入了 files（新接口），则回调数组
    if (Array.isArray(files) && typeof onFilesChange === "function") {
      onFilesChange(nextArr);
      return;
    }
    // 旧接口兼容：回调 (f1, f2)
    if (typeof onFilesChange === "function") {
      const f1 = nextArr[0] || null;
      const f2 = nextArr[1] || null;
      onFilesChange(f1, f2);
    }
  }, [files, onFilesChange]);

  const handleDragOver = useCallback((e) => {
    e.preventDefault();
    if (disabled) return;
    e.dataTransfer.dropEffect = "copy";
    setIsDragging(true);
  }, [disabled]);

  const handleDragLeave = useCallback((e) => {
    e.preventDefault();
    if (disabled) return;
    setIsDragging(false);
  }, [disabled]);

  const handleDrop = useCallback((e) => {
    e.preventDefault();
    if (disabled) return;
    setIsDragging(false);
    const dropped = Array.from(e.dataTransfer?.files || []);
    if (!dropped.length) return;
    const valid = dropped.filter(f => isAcceptedExcel(f, accept));
    if (valid.length === 0) return;
    const next = effectiveFiles.slice();
    for (const f of valid) {
      const idx = next.findIndex(x => !x);
      if (idx === -1) break; // 槽位已满
      next[idx] = f;
    }
    emitChange(next);
  }, [accept, disabled, effectiveFiles, emitChange]);

  const containerStyle = useMemo(() => ({
    border: "2px dashed #b7c8ff",
    background: isDragging ? "#f1f6ff" : "#fafcff",
    borderRadius: 8,
    padding: 16,
    transition: "background 0.12s ease-in-out",
    cursor: disabled ? "not-allowed" : "pointer",
    userSelect: "none",
    ...style,
  }), [isDragging, disabled, style]);

  const handlePickAt = useCallback((index, f) => {
    if (disabled) return;
    if (f && !isAcceptedExcel(f, accept)) return;
    const next = effectiveFiles.slice();
    next[index] = f || null;
    emitChange(next);
  }, [accept, disabled, effectiveFiles, emitChange]);

  const handleClearAt = useCallback((index) => {
    if (disabled) return;
    const next = effectiveFiles.slice();
    next[index] = null;
    emitChange(next);
  }, [disabled, effectiveFiles, emitChange]);

  return (
    <div
      className={className}
      style={containerStyle}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      role="button"
      aria-disabled={disabled}
      tabIndex={0}
    >
      <div style={{ color: "#445", marginBottom: 12 }}>
        将最多 {slots} 个 Excel 拖拽到此处，或使用下方按钮选择文件
        <div style={{ fontSize: 12, color: "#7a7f8c", marginTop: 4 }}>支持：.xlsx, .xls</div>
      </div>
      <UploadForm
        slots={slots}
        files={effectiveFiles}
        accept={accept}
        disabled={disabled}
        onPickAt={handlePickAt}
        onClearAt={handleClearAt}
      />
    </div>
  );
}