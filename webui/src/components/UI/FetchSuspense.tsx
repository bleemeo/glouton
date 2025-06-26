/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { FC, ReactNode } from "react";
import { AxiosError } from "axios";

import QueryError from "./QueryError";
import { Loading } from "./Loading";

interface FetchSuspenseProps {
  children: (props: any) => ReactNode;
  isLoading: boolean;
  error?: object | boolean | AxiosError | null;
  loadingComponent?: ReactNode;
  fallbackComponent?: ReactNode;
  [key: string]: any;
}

const FetchSuspense: FC<FetchSuspenseProps> = ({
  children,
  isLoading,
  error,
  loadingComponent,
  fallbackComponent,
  ...other
}) => {
  if (isLoading) {
    return <>{loadingComponent || <Loading size="xl" />}</>;
  } else if (error) {
    return <>{fallbackComponent || <QueryError />}</>;
  } else {
    return <>{children({ ...other })}</>;
  }
};

export default FetchSuspense;
