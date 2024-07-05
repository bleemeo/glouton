import React from "react";

type FaIconProps = {
  icon: string;
};

const FaIcon: React.FC<FaIconProps> = ({ icon }) => (
  <i className={`${icon}`} aria-hidden="true" />
);

export default FaIcon;
