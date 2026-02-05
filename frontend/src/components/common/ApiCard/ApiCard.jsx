import React from "react";
import "./ApiCard.css";
const ApiCard = ({ title, description, link, icon }) => {
  const handleClick = () => {
    if (link) {
      if (link.startsWith("http")) {
        window.location.href = link;
      } else {
        window.location.href = link;
      }
    }
  };

  const handleKeyDown = (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      handleClick();
    }
  };

  return (
    <div className="api-card" role="button" tabIndex={0} onClick={handleClick} onKeyDown={handleKeyDown}>
      {icon && (
        <div className="api-card__icon" aria-hidden>
          {typeof icon === "string" ? <span>{icon}</span> : icon}
        </div>
      )}
      <div className="api-card__body">
        <div className="api-card__title">{title}</div>
        {description && <div className="api-card__desc">{description}</div>}
      </div>
    </div>
  );
};

export default ApiCard;


