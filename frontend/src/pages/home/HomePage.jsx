import React, { useMemo, useState } from "react";
import "./HomePage.css";
import Header from "../../components/common/Header/Header.jsx";
import ApiCard from "../../components/common/ApiCard/ApiCard.jsx";
import ApiCardContainer from "../../components/common/ApiCard/ApiCardContainer.jsx";

const HomePage = ({ onEnterCompare }) => {
  const [query, setQuery] = useState("");

  const handleEnter = () => {
    if (typeof onEnterCompare === "function") {
      onEnterCompare();
    } else {
      window.location.hash = "#compare";
    }
  };

  const features = useMemo(
    () => [
      {
        key: "compare",
        title: "Excel 比对",
        description: "上传两份表格，找出减少/增加/差异项并导出结果。",
        link: "#compare",
        icon: "🟦",
      },
      {
        key: "placeholder",
        title: "占位测试",
        description: "指向空页面，用于验证入口/路由是否正常。",
        link: "#placeholder",
        icon: "🧪",
      },
    ],
    [],
  );

  const filtered = useMemo(() => {
    const q = String(query || "").trim().toLowerCase();
    if (!q) return features;
    return features.filter((f) => {
      const hay = `${f.title || ""} ${f.description || ""}`.toLowerCase();
      return hay.includes(q);
    });
  }, [features, query]);

  return (
    <div className="home-page">
      <Header />
      <main className="home-main">
        <section className="home-hero">
          <div>
            <div className="home-hero__pill">Excel 处理工具</div>
            <h1>上传两份 Excel，快速比对差异</h1>
            <p className="home-hero__desc">
              支持差异导出与预览。
            </p>
            <div className="home-hero__actions">
              <button className="btn-primary" onClick={handleEnter}>开始比对</button>
              <button className="btn-ghost" onClick={() => window.location.hash = "#compare"}>直接进入 Compare</button>
            </div>
            <div className="home-hero__meta">无需登录即可体验，上传仅在本地发送到后端进行比对。</div>
          </div>
        </section>

        <div className="home-feature-header">
          <div className="home-feature-title">功能入口</div>
          <div className="home-search">
            <input
              className="home-search__input"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索功能（例如：比对 / 占位）"
              aria-label="搜索功能"
            />
            <button
              className={`home-search__clear ${query ? "" : "is-hidden"}`}
              onClick={() => setQuery("")}
              aria-label="清空搜索"
              disabled={!query}
              type="button"
            >
              清空
            </button>
          </div>
        </div>

        <ApiCardContainer title={null} columns={2}>
          {filtered.map((f) => (
            <ApiCard key={f.key} title={f.title} description={f.description} link={f.link} icon={f.icon} />
          ))}
        </ApiCardContainer>

        {filtered.length === 0 ? (
          <div className="home-empty">没有匹配的功能入口</div>
        ) : null}
      </main>
    </div>
  );
};

export default HomePage;

