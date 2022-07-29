# Charts Helm Glouton


## How to use it ?

The charts has been made with helm 3. To run it you must clone the repo and run the following command :

```bash
    helm upgrade --install glouton ./glouton --set bleemeo.account_id="<youridhere>" --set bleemeo.registration_key="<yourprivatetokenhere>" --set namespace="default"
