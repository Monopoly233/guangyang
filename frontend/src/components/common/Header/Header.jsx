import React from "react";
import "./Header.css";
const Header = () => {
  return (
    <header className="site-header">
      <div className="site-header__container">
        <div className="site-header__left">
          <a className="site-header__logo" href="/">nanyang</a>
        </div>
        <div className="site-header__right" />
      </div>
    </header>
  );
};

export default Header;


