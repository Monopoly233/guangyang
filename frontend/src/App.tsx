// @ts-nocheck
import { useCallback, useEffect, useState } from "react";
import "./App.css";
import HomePage from "./pages/home/HomePage.jsx";
import ComparePage from "./pages/compare/ComparePage.jsx";
import PlaceholderPage from "./pages/placeholder/PlaceholderPage.jsx";

function App() {
  const [page, setPage] = useState(() => {
    const h = window.location.hash;
    if (h === "#compare") return "compare";
    if (h === "#placeholder") return "placeholder";
    return "home";
  });

  const goHome = useCallback(() => {
    setPage("home");
    window.location.hash = "";
  }, []);

  const goCompare = useCallback(() => {
    setPage("compare");
    window.location.hash = "#compare";
  }, []);

  useEffect(() => {
    const handler = () => {
      const h = window.location.hash;
      setPage(h === "#compare" ? "compare" : h === "#placeholder" ? "placeholder" : "home");
    };
    window.addEventListener("hashchange", handler);
    return () => window.removeEventListener("hashchange", handler);
  }, []);

  if (page === "compare") {
    return <ComparePage onBack={goHome} />;
  }
  if (page === "placeholder") {
    return <PlaceholderPage onBack={goHome} />;
  }
  return <HomePage onEnterCompare={goCompare} />;
}

export default App;
