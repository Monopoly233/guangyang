// Dev(Vite): direct to local ports. Prod(Docker+Nginx): use same-origin reverse proxy.
const isProd = !!import.meta.env?.PROD;
const DEFAULT_GO_BASE = isProd ? "/api" : "http://localhost:8080";

export const GO_API_BASE = import.meta.env?.VITE_GO_API_BASE || DEFAULT_GO_BASE;

async function handleJSONResponse(resp) {
  const text = await resp.text();
  let data;
  try {
    data = text ? JSON.parse(text) : {};
  } catch (_) {
    data = { message: text || "服务返回了不可解析的结果" };
  }
  if (!resp.ok) {
    const msg = data?.detail || data?.message || resp.statusText || "请求失败";
    throw new Error(msg);
  }
  return data;
}

export async function createPendingBillingEvent(amount, metadata = {}) {
  const resp = await fetch(`${GO_API_BASE}/billing/pending`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      amount: Number(amount) || 0,
      apiCall: metadata.apiCall,
      metadata,
      idempotencyKey: metadata.idempotencyKey,
    }),
  });
  return handleJSONResponse(resp);
}

export async function deductWithKey(amount, { apiCall, idempotencyKey } = {}) {
  const resp = await fetch(`${GO_API_BASE}/billing/deduct`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      amount: Number(amount) || 0,
      apiCall,
      idempotencyKey,
    }),
  });
  return handleJSONResponse(resp);
}

// --- Pay-gated compare jobs (Go backend) ---

export async function createCompareJob(file1, file2) {
  const form = new FormData();
  form.append("file1", file1);
  form.append("file2", file2);
  const resp = await fetch(`${GO_API_BASE}/compare/jobs`, {
    method: "POST",
    body: form,
    credentials: "include",
  });
  return handleJSONResponse(resp);
}

export async function getCompareJob(jobId) {
  const resp = await fetch(`${GO_API_BASE}/compare/jobs/${encodeURIComponent(jobId)}`, {
    method: "GET",
    credentials: "include",
  });
  return handleJSONResponse(resp);
}

export async function downloadCompareExport(jobId) {
  // Prefer OSS direct download: ask backend for a signed URL (JSON)
  const metaResp = await fetch(`${GO_API_BASE}/compare/jobs/${encodeURIComponent(jobId)}/export?format=json`, {
    method: "GET",
    credentials: "include",
    headers: { Accept: "application/json" },
  });
  const meta = await handleJSONResponse(metaResp);
  const rawUrl = (meta?.url || "").trim();
  const filename = meta?.filename || "比对结果.xlsx";
  if (!rawUrl) {
    throw new Error("未获取到下载链接");
  }
  // If backend returns a relative path (e.g. /compare/jobs/.../export), prefix it with GO_API_BASE
  const directUrl = rawUrl.startsWith("/") ? `${GO_API_BASE}${rawUrl}` : rawUrl;

  // Fetch from OSS directly (no credentials/cookies)
  const resp = await fetch(directUrl, { method: "GET", mode: "cors" });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(text || resp.statusText || "下载失败");
  }
  const blob = await resp.blob();
  return { blob, filename, directUrl };
}

export async function cancelCompareJob(jobId) {
  const resp = await fetch(`${GO_API_BASE}/compare/jobs/${encodeURIComponent(jobId)}/cancel`, {
    method: "POST",
    credentials: "include",
  });
  return handleJSONResponse(resp);
}


