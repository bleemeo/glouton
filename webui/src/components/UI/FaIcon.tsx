import { FC } from "react";

type FaIconProps = {
  icon: string;
};

const FaIcon: FC<FaIconProps> = ({ icon }) => (
  <i className={`${icon}`} aria-hidden="true" />
);

export default FaIcon;
