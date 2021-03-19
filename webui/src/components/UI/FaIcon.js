import React from "react";
import PropTypes from "prop-types";

const FaIcon = ({ icon }) => <i className={`${icon}`} aria-hidden="true" />;

FaIcon.propTypes = {
  icon: PropTypes.string.isRequired,
};

export default FaIcon;
