import classnames from "classnames";
import React, { useState } from "react";
import {
  Modal as ModalStrap,
  ModalHeader,
  ModalBody,
  ModalFooter,
} from "reactstrap";

type ModalProps = {
  title: string | React.ReactNode;
  disabledMainBtn?: boolean;
  disabledCloseBtn?: boolean;
  mainBtnLabel?: string;
  mainBtnAction?: () => void;
  closeLabel?: string;
  closeAction: () => void;
  mainBtnActionType?: "delete";
  className?: string;
  size?: "sm" | "lg" | "xl";
  children: React.ReactNode;
};

export default function Modal({
  title,
  disabledMainBtn = false,
  disabledCloseBtn = false,
  mainBtnLabel,
  mainBtnAction,
  closeLabel,
  closeAction,
  mainBtnActionType,
  className,
  size,
  children,
}: ModalProps) {
  const [isOpen, setIsOpen] = useState(true);

  function handleToggle() {
    setIsOpen(!isOpen);
    if (!isOpen) closeAction();
  }

  let mainBtnClassName = "";
  if (mainBtnActionType && mainBtnActionType === "delete") {
    mainBtnClassName = "btn-danger";
  } else {
    mainBtnClassName = "btn-primary";
  }

  let mainBtn: React.ReactNode = null;
  if (mainBtnAction && mainBtnLabel) {
    mainBtn = (
      <button
        type="button"
        className={"btn " + mainBtnClassName}
        onClick={mainBtnAction}
        disabled={disabledMainBtn}
      >
        {mainBtnLabel}
      </button>
    );
  }

  let footer: React.ReactNode = null;
  if (mainBtn || closeLabel) {
    footer = (
      <ModalFooter>
        <button
          type="button"
          className="btn btn-secondary"
          data-dismiss="modal"
          onClick={handleToggle}
          disabled={disabledCloseBtn}
        >
          {closeLabel}
        </button>
        {mainBtn}
      </ModalFooter>
    );
  }

  return (
    <ModalStrap
      isOpen={isOpen}
      toggle={handleToggle}
      className={classnames(`modal-dialog ${className}`)}
      size={size}
    >
      <ModalHeader toggle={handleToggle}>
        <span className="modal-title" id="myModalLabel">
          {typeof title === "string" ? <h4>{title}</h4> : title}
        </span>
      </ModalHeader>
      <ModalBody>{children}</ModalBody>
      {footer}
    </ModalStrap>
  );
}
