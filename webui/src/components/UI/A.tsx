/* eslint-disable @typescript-eslint/no-explicit-any */

/*
  Several articles said that <a> should only be used to link to another page
  and that we should use <button> instead for SPA link or action link.
  But if we use a <button> a lot of style gets broken, event with .btn-link
  from bootstrap. So we use <a> with tab-index="0" and a special onKeyPress
  handler to have the same behavior than with a button, aka onClick is
  triggered when Enter or Space is pressed.
 */

const handleKeyPress = (
  event: { charCode: number },
  onClick: (arg0: any) => void,
) => {
  // simulate a click when the user press Enter or Space with the focus on the link
  if (event.charCode === 13 || event.charCode === 32) {
    onClick(event);
  }
  // TODO should we do smth here
};

const A = (props: {
  [x: string]: any;
  className?: string;
  onClick: any;
  children: any;
}) => {
  const { className, onClick, children, ...btnProps } = props;
  const onKeyPress = handleKeyPress;
  return (
    <a
      tabIndex={0}
      className={`${className ? className : ""}`}
      onClick={onClick}
      onKeyUp={(e) => onKeyPress(e, onClick)}
      {...btnProps}
    >
      {children}
    </a>
  );
};

export default A;
