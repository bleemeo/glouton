/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { ReactNode } from "react";
import QueryError from "./QueryError";
import Loading from "./Loading";
import { AxiosError } from "axios";

// Définir l'interface pour les props
interface FetchSuspenseProps {
  children: (props: any) => ReactNode;
  isLoading: boolean;
  error?: object | boolean | AxiosError | null;
  loadingComponent?: ReactNode;
  fallbackComponent?: ReactNode;
  [key: string]: any; // Pour d'autres props arbitraires
}

// Composant fonctionnel en TypeScript
const FetchSuspense: React.FC<FetchSuspenseProps> = ({
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
