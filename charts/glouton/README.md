# Glouton Helm Chart


## How to use it ?

The charts has been made with helm 3. To run it you can clone the repo and run the following command:

```sh
    helm upgrade --install glouton ./glouton \
    --set account_id="your_account_id" \
    --set registration_key="your_registration_key" \
    --set config.kubernetes.clustername="my_k8s_cluster_name" \
    --set namespace="default"
```
