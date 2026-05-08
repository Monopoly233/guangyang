// Dev(Vite): direct to local ports. Prod(Docker+Nginx): use same-origin reverse proxy.
const isProd = !!import.meta.env?.PROD;
const DEFAULT_GO_BASE = isProd ? "/api" : "http://localhost:8080";

export const GO_API_BASE = import.meta.env?.VITE_GO_API_BASE || DEFAULT_GO_BASE;

function isHTMLResponse(text, resp) {
  const contentType = (resp?.headers?.get("content-type") || "").toLowerCase();
  const body = (text || "").trim().toLowerCase();
  return contentType.includes("text/html") || body.startsWith("<!doctype html") || body.startsWith("<html");
}

function friendlyHTTPError(resp, text) {
  const status = Number(resp?.status || 0);
  const body = (text || "").toLowerCase();
  if (status === 413 || body.includes("request entity too large")) {
    return "上传文件太大，服务器当前拒绝接收。请压缩文件，或让管理员调大 Nginx 的 client_max_body_size 和 COMPARE_MAX_UPLOAD_MB。";
  }
  if (status === 502 || body.includes("bad gateway") || body.includes("cloudflare")) {
    return "服务暂时不可用：网关没有连上后端服务。请稍后重试，或联系管理员检查 Nginx/Cloudflare 到后端的代理配置。";
  }
  if (status === 504 || body.includes("gateway timeout")) {
    return "服务处理超时：文件可能较大或后端繁忙。请稍后重试。";
  }
  if (isHTMLResponse(text, resp)) {
    return `服务返回了错误页面（HTTP ${status || "未知"}），请稍后重试。`;
  }
  return "";
}

function normalizeFetchError(err) {
  if (err?.name === "TypeError") {
    return new Error("无法连接服务，请检查网络或后端服务是否已启动。");
  }
  return err;
}

async function handleJSONResponse(resp) {
  const text = await resp.text();
  let data;
  try {
    data = text ? JSON.parse(text) : {};
  } catch (_) {
    data = { message: friendlyHTTPError(resp, text) || text || "服务返回了不可解析的结果" };
  }
  if (!resp.ok) {
    const msg = friendlyHTTPError(resp, text) || data?.detail || data?.message || resp.statusText || "请求失败";
    throw new Error(msg);
  }
  if (isHTMLResponse(text, resp)) {
    throw new Error(friendlyHTTPError(resp, text));
  }
  return data;
}

async function fetchWithFriendlyError(url, options) {
  try {
    return await fetch(url, options);
  } catch (err) {
    throw normalizeFetchError(err);
  }
}

export async function createPendingBillingEvent(amount, metadata = {}) {
  const resp = await fetchWithFriendlyError(`${GO_API_BASE}/billing/pending`, {
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
  const resp = await fetchWithFriendlyError(`${GO_API_BASE}/billing/deduct`, {
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
  const resp = await fetchWithFriendlyError(`${GO_API_BASE}/compare/jobs`, {
    method: "POST",
    body: form,
    credentials: "include",
  });
  return handleJSONResponse(resp);
}

export async function getCompareJob(jobId) {
  const resp = await fetchWithFriendlyError(`${GO_API_BASE}/compare/jobs/${encodeURIComponent(jobId)}`, {
    method: "GET",
    credentials: "include",
  });
  return handleJSONResponse(resp);
}

export async function downloadCompareExport(jobId) {
  // Prefer OSS direct download: ask backend for a signed URL (JSON)
  const metaResp = await fetchWithFriendlyError(`${GO_API_BASE}/compare/jobs/${encodeURIComponent(jobId)}/export?format=json`, {
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
  const resp = await fetchWithFriendlyError(directUrl, { method: "GET", mode: "cors" });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(friendlyHTTPError(resp, text) || text || resp.statusText || "下载失败");
  }
  const contentType = (resp.headers.get("content-type") || "").toLowerCase();
  if (contentType.includes("text/html")) {
    const text = await resp.text();
    throw new Error(friendlyHTTPError(resp, text));
  }
  const blob = await resp.blob();
  return { blob, filename, directUrl };
}

export async function cancelCompareJob(jobId) {
  const resp = await fetchWithFriendlyError(`${GO_API_BASE}/compare/jobs/${encodeURIComponent(jobId)}/cancel`, {
    method: "POST",
    credentials: "include",
  });
  return handleJSONResponse(resp);
}


