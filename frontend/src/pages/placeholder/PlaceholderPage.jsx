import React from "react";
import Header from "../../components/common/Header/Header.jsx";
import "./PlaceholderPage.css";

const PlaceholderPage = ({ onBack }) => {
  return (
    <div className="placeholder-page">
      <Header />
      <main className="placeholder-main">
        <h3>占位页面（测试用）</h3>
        <p className="placeholder-desc">
          这里暂时不做任何业务逻辑，用来验证“功能入口/路由/页面跳转”是否正常。
        </p>
        <div className="placeholder-actions">
          <button
            className="btn-ghost"
            onClick={() => (typeof onBack === "function" ? onBack() : (window.location.hash = ""))}
          >
            返回首页
          </button>
        </div>
      </main>
    </div>
  );
};

export default PlaceholderPage;

