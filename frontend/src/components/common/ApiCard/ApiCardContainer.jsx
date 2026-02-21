import React from "react";
import "./ApiCardContainer.css";

const ApiCardContainer = ({ title, children, columns = 3 }) => {
  const gridStyle = {
    gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
  };
  return (
    <div className="api-card-container">
      {title && <div className="api-card-container__title">{title}</div>}
      <div className="api-card-container__grid" style={gridStyle}>
        {children}
      </div>
    </div>
  );
};

export default ApiCardContainer;


