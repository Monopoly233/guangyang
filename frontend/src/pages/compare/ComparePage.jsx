import React from "react";
import * as XLSX from "xlsx";
import QRCode from "qrcode";
import Header from "../../components/common/Header/Header.jsx";
import "./ComparePage.css";
import DragUploadArea from "../../components/common/DragUploadArea/DragUploadArea.jsx";
import { createCompareJob, getCompareJob, downloadCompareExport, cancelCompareJob } from "../../api/paidApi.js";

class ComparePage extends React.Component {
  constructor(props) {
    super(props);
    this.state = {
      file1: null,
      file2: null,
      loading: false,
      startLocked: false,
      error: undefined,
      jobId: undefined,
      jobStatus: undefined,
      payAmount: 0,
      codeUrl: undefined,
      qrDataUrl: undefined,
      qrError: undefined,
      showPayModal: false,
      previewSheets: [], // [{ name, html }]
      lastExport: null, // { blob, filename, url }
    };
    this._pollTimer = null;
    this._lastQrCodeUrl = null;
    this._isMounted = false;
  }

  async componentDidMount() {
    this._isMounted = true;
    // 已移除 demo 用户/余额展示，这里不再拉取 profile
  }

  componentWillUnmount() {
    this._isMounted = false;
    this.stopPolling();
    const le = this.state.lastExport;
    if (le && le.url) {
      URL.revokeObjectURL(le.url);
    }
  }

  handleFile1 = (e) => this.setState({ file1: e.target.files[0] || null });
  handleFile2 = (e) => this.setState({ file2: e.target.files[0] || null });

  ensureQr = async (codeUrl) => {
    const u = (codeUrl || "").trim();
    if (!u) return;
    if (u === this._lastQrCodeUrl && this.state.qrDataUrl) return;
    this._lastQrCodeUrl = u;
    try {
      const dataUrl = await QRCode.toDataURL(u, {
        errorCorrectionLevel: "M",
        margin: 1,
        width: 240,
      });
      if (!this._isMounted) return;
      this.setState({ qrDataUrl: dataUrl, qrError: undefined });
    } catch (err) {
      if (!this._isMounted) return;
      this.setState({ qrDataUrl: undefined, qrError: err?.message || "生成二维码失败" });
    }
  };

  cancelPayment = async () => {
    const jobId = this.state.jobId;
    // Always hide modal immediately for responsiveness.
    this.setState({ showPayModal: false, error: undefined });

    this.stopPolling();

    if (!jobId) {
      this.setState({ jobStatus: "cancelled", startLocked: false, error: "订单已取消" });
      return;
    }

    try {
      await cancelCompareJob(jobId);
      this.setState({
        jobStatus: "cancelled",
        showPayModal: false,
        startLocked: false,
        error: "订单已取消",
        codeUrl: undefined,
        qrDataUrl: undefined,
        qrError: undefined,
      });
    } catch (err) {
      // If cancel failed, resume polling to keep UX consistent.
      this.setState({ error: err?.message || "取消失败，已恢复轮询" });
      this.startPolling(jobId);
    }
  };

  stopPolling = () => {
    if (this._pollTimer) {
      clearTimeout(this._pollTimer);
      this._pollTimer = null;
    }
    this._pollDelayMs = undefined;
    this._pollFailures = 0;
  };

  startPolling = (jobId) => {
    this.stopPolling();
    this._pollDelayMs = 900;
    this._pollFailures = 0;

    const pollOnce = async () => {
      try {
        const j = await getCompareJob(jobId);
        const status = j?.status;
        const nextCodeUrl = j?.code_url;
        this.setState({
          jobStatus: status,
          codeUrl: nextCodeUrl,
          showPayModal: status === "awaiting_payment",
          payAmount: j?.amount ?? 0,
        });
        if (nextCodeUrl) {
          // Generate QR lazily when code_url becomes available
          this.ensureQr(nextCodeUrl);
        }

        if (status === "failed") {
          this.stopPolling();
          this.setState({ loading: false, startLocked: false, error: j?.error || "任务失败" });
          return;
        }

        if (status === "cancelled") {
          this.stopPolling();
          this.setState({ loading: false, startLocked: false, showPayModal: false, error: "订单已取消" });
          return;
        }

        if (status === "ready") {
          this.stopPolling();
          this.setState({ showPayModal: false });
          try {
            await this.fetchAndPreview(jobId);
          } catch (err) {
            this.setState({ error: err?.message || "获取结果失败" });
          } finally {
            this.setState({ startLocked: false });
          }
          return;
        }

        // Schedule next poll (adaptive)
        if (status === "awaiting_payment") {
          this._pollDelayMs = 1500;
        } else {
          // processing: exponential-ish backoff to reduce load
          const d = Number(this._pollDelayMs || 900);
          this._pollDelayMs = Math.min(5000, Math.floor(d * 1.25));
        }
        this._pollTimer = setTimeout(pollOnce, this._pollDelayMs);
      } catch (err) {
        // 轮询错误不立刻终止，避免偶发网络抖动
        this.setState({ error: err.message });
        const failures = Number(this._pollFailures || 0) + 1;
        this._pollFailures = failures;
        const base = Number(this._pollDelayMs || 900);
        const next = Math.min(8000, Math.floor(base * (1.4 + Math.min(3, failures) * 0.2)));
        this._pollDelayMs = next;
        this._pollTimer = setTimeout(pollOnce, next);
      }
    };

    this._pollTimer = setTimeout(pollOnce, this._pollDelayMs);
  };

  fetchAndPreview = async (jobId) => {
    this.setState({ loading: true, error: undefined });
    try {
      const { blob, filename, directUrl } = await downloadCompareExport(jobId);
      const ab = await blob.arrayBuffer();
      const wb = XLSX.read(ab, { type: "array" });
      const previewSheets = wb.SheetNames.map((name) => {
        const ws = wb.Sheets[name];
        const html = XLSX.utils.sheet_to_html(ws, { header: "", footer: "" });
        return { name, html };
      });

      const prev = this.state.lastExport;
      if (prev && prev.url) URL.revokeObjectURL(prev.url);

      const url = URL.createObjectURL(blob);
      this.setState({ previewSheets, lastExport: { blob, filename: filename || "comparison_result.xlsx", url, directUrl } });
    } finally {
      this.setState({ loading: false });
    }
  };

  // 已改为自动扣费，不再需要手动确认

  renderTable = (title, table) => {
    if (!table || !Array.isArray(table) || table.length === 0) return null;
    const [headers, ...rows] = table;
    return (
      <div style={{ marginTop: 16 }}>
        <h4>{title}</h4>
        <div style={{ overflowX: "auto" }}>
          <table border="1" cellPadding="6" style={{ borderCollapse: "collapse", minWidth: 600 }}>
            <thead>
              <tr>
                {headers.map((h, idx) => (
                  <th key={idx}>{String(h)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i}>
                  {row.map((cell, j) => (
                    <td key={j}>{cell === null || cell === undefined ? "" : String(cell)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    );
  };

  render() {
    return (
      <div>
        <Header />
        <div className="compare-container">
          <h3>Excel 对比</h3>
          <div style={{ marginTop: 8, marginBottom: 12, color: "#475569", fontSize: 14, lineHeight: 1.6 }}>
            <div>请上传两份<strong>类别相同</strong>的 Excel（同一业务/同一字段体系），并且两份表里都包含同一列“编号/编码”等主键列用于匹配。</div>
            <div>系统会在同一个 <strong>.xlsx</strong> 文件中输出 3 张工作表：<strong>增加项</strong>（只在<strong>第二个</strong> Excel/表B）、<strong>减少项</strong>（只在<strong>第一个</strong> Excel/表A）、<strong>变动项目</strong>（两份表中同一主键但内容有变化）。</div>
          </div>
          <form>
            <div style={{ marginBottom: 12 }}>
              <DragUploadArea
                file1={this.state.file1}
                file2={this.state.file2}
                accept=".xlsx,.xls"
                onFilesChange={(f1, f2) => this.setState({ file1: f1 || null, file2: f2 || null })}
              />
            </div>
            <div className="compare-actions">
              <button onClick={async (e) => {
                e.preventDefault();
                if (this.state.startLocked) return;
                if (!this.state.file1 || !this.state.file2) { this.setState({ error: "请先选择两个Excel文件" }); return; }
                try {
                  this.setState({
                    loading: true,
                    startLocked: true,
                    error: undefined,
                    previewSheets: [],
                    lastExport: null,
                    jobId: undefined,
                    jobStatus: "processing",
                    showPayModal: false,
                    codeUrl: undefined,
                    qrDataUrl: undefined,
                    qrError: undefined,
                  });

                  const job = await createCompareJob(this.state.file1, this.state.file2);
                  const jobId = job?.jobId;
                  if (!jobId) throw new Error("创建任务失败：未返回 jobId");
                  this.setState({ jobId, jobStatus: job?.status || "processing" });
                  this.startPolling(jobId);
                } catch (err) {
                  this.setState({ startLocked: false, error: err.message });
                } finally {
                  // loading 由轮询/下载阶段驱动
                  this.setState({ loading: false });
                }
              }} disabled={this.state.loading || this.state.startLocked}>
                {this.state.loading || this.state.startLocked ? "进行中..." : "开始比对"}
              </button>
              <button onClick={(e) => {
                e.preventDefault();
                const le = this.state.lastExport;
                if (!le) { this.setState({ error: "请先执行比对并导出" }); return; }
                // Prefer OSS direct URL for large downloads; fallback to in-memory blob url.
                const href = (le.directUrl || le.url || "").trim();
                if (!href) { this.setState({ error: "下载链接不存在" }); return; }
                const a = document.createElement('a');
                a.href = href;
                a.rel = "noopener";
                // cross-origin download attribute may be ignored by browsers; OSS response headers will control filename.
                a.download = le.filename || "比对结果.xlsx";
                document.body.appendChild(a);
                a.click();
                a.remove();
              }} disabled={!this.state.lastExport}>下载结果</button>
            </div>
          </form>
          <div className="info-box">
            <div style={{ fontWeight: 600 }}>当前收费：0.01 元/次</div>
            {this.state.jobId ? (
              <div style={{ marginTop: 6, color: "#334155" }}>
                Job：{this.state.jobId}（状态：{this.state.jobStatus || "unknown"}）
              </div>
            ) : null}
          </div>
          {this.state.error && <div className="error-box">{this.state.error}</div>}

          {this.state.showPayModal ? (
            <div className="pay-modal__mask" role="dialog" aria-modal="true">
              <div className="pay-modal">
                <div className="pay-modal__title">请先完成支付</div>
                <div className="pay-modal__desc">请使用微信扫一扫支付 {this.state.payAmount} 元，支付成功后会自动返回结果。</div>
                <div className="pay-modal__content">
                  <div className="pay-modal__qrPane">
                    <div className="pay-modal__codeLabel">二维码</div>
                    {this.state.qrDataUrl ? (
                      <img className="pay-modal__qr" src={this.state.qrDataUrl} alt="微信支付二维码" />
                    ) : (
                      <div className="pay-modal__qrPlaceholder">
                        {this.state.codeUrl ? (this.state.qrError ? this.state.qrError : "正在生成二维码...") : "等待后端返回二维码信息..."}
                      </div>
                    )}
                  </div>
                </div>
                <div className="pay-modal__actions">
                  <button onClick={(e) => { e.preventDefault(); this.cancelPayment(); }}>
                    取消并关闭
                  </button>
                </div>
              </div>
            </div>
          ) : null}

          {/* 直接预览导出的 Excel（按工作表渲染） */}
          {this.state.previewSheets && this.state.previewSheets.length > 0 && (
            <div className="preview-wrapper">
              {this.state.previewSheets.map((s, idx) => (
                <div key={idx} className="preview-sheet">
                  <h4>{s.name}</h4>
                  <div style={{ overflowX: "auto" }} dangerouslySetInnerHTML={{ __html: s.html }} />
                </div>
              ))}
            </div>
          )}
          {/* 若仍需保留旧的 JSON 渲染，可取消以下三行注释 */}
          {/* {this.renderTable("减少项 (只在文件1)", this.state.result && this.state.result.reduced)} */}
          {/* {this.renderTable("增加项 (只在文件2)", this.state.result && this.state.result.increased)} */}
          {/* {this.renderTable("差异项 (两侧各一行)", this.state.result && this.state.result.different)} */}
        </div>
      </div>
    );
  }
}

export default ComparePage;


