# platform-ingress

## ADDED Requirements

### Requirement: Every publicly reachable platform route SHALL have an entry in the checked-in ingress manifests

The production ingress routes a hand-maintained list of paths to the platform service. A route that the code
exposes publicly and the manifest does not carry is a route that answers 404 in production while passing every
test.

#### Scenario: The manifest carries what the code exposes
- **WHEN** the platform registers a route that is reachable without a platform credential
- **THEN** the checked-in ingress manifests carry a path entry routing it to the platform service

#### Scenario: Applying the manifest does not remove a working route
- **WHEN** the checked-in production manifest is applied to a cluster
- **THEN** every public platform route that worked before the apply still works after it

### Requirement: A public platform route with no ingress entry SHALL fail a test rather than a deployment

#### Scenario: The fence catches an unrouted public route
- **WHEN** a new public platform route is added without a corresponding ingress entry
- **THEN** a test fails, naming the route and the manifest it is missing from

#### Scenario: An internal route is not required to be public
- **WHEN** a route requires a platform credential or is reached only from inside the cluster
- **THEN** the fence does not require an ingress entry for it, and the route's classification is declared
  rather than inferred
