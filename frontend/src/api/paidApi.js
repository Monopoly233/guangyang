// Dev(Vite): direct to local ports. Prod(Docker+Nginx): use same-origin reverse proxy.
const isProd = !!import.meta.env?.PROD;
const DEFAULT_PY_BASE = isProd ? "/py" : "http://localhost:8000";
const DEFAULT_GO_BASE = isProd ? "/api" : "http://localhost:8080";

export const PY_API_BASE = import.meta.env?.VITE_PY_API_BASE || DEFAULT_PY_BASE;
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

export async function estimateFee(files, params = {}) {
  const form = new FormData();
  (files || []).forEach((f) => form.append("files", f));
  const query = new URLSearchParams();
  if (params.pricing_mode) query.set("pricing_mode", params.pricing_mode);
  if (params.rate_per_mb != null) query.set("rate_per_mb", params.rate_per_mb);
  if (params.rate_per_1k_rows != null) query.set("rate_per_1k_rows", params.rate_per_1k_rows);

  const resp = await fetch(`${PY_API_BASE}/feeguest/estimate?${query.toString()}`, {
    method: "POST",
    body: form,
    credentials: "include",
  });
  return handleJSONResponse(resp);
}

export async function compareExcel(file1, file2, { idempotencyKey } = {}) {
  const form = new FormData();
  form.append("file1", file1);
  form.append("file2", file2);
  const headers = {};
  if (idempotencyKey) headers["X-Idempotency-Key"] = idempotencyKey;
  const resp = await fetch(`${PY_API_BASE}/compare/`, {
    method: "POST",
    headers,
    body: form,
    credentials: "include",
  });
  return handleJSONResponse(resp);
}

export async function compareExport(file1, file2, { idempotencyKey } = {}) {
  const form = new FormData();
  form.append("file1", file1);
  form.append("file2", file2);
  const headers = {};
  if (idempotencyKey) headers["X-Idempotency-Key"] = idempotencyKey;
  const resp = await fetch(`${PY_API_BASE}/compare/export`, {
    method: "POST",
    headers,
    body: form,
    credentials: "include",
  });
  if (!resp.ok) {
    const errText = await resp.text();
    throw new Error(errText || resp.statusText);
  }
  const blob = await resp.blob();
  const cd = resp.headers.get("Content-Disposition") || "";
  const match = /filename\*=UTF-8''([^;]+)/i.exec(cd);
  const fallback = /filename="([^"]+)"/i.exec(cd);
  const filename = match ? decodeURIComponent(match[1]) : fallback ? fallback[1] : "compare.xlsx";
  return { blob, filename };
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
  const resp = await fetch(`${GO_API_BASE}/compare/jobs/${encodeURIComponent(jobId)}/export`, {
    method: "GET",
    credentials: "include",
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(text || resp.statusText || "下载失败");
  }
  const blob = await resp.blob();
  const cd = resp.headers.get("Content-Disposition") || "";
  const fallback = /filename="?([^\";]+)"?/i.exec(cd);
  const filename = fallback ? fallback[1] : "comparison_result.xlsx";
  return { blob, filename };
}


