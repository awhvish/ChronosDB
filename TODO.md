# TODO - Add Read Consistency Options (Linearizable reads)

## Task
Add a ?consistency=strong query parameter in handleGet() which would redirect all Get() requests to the leader, ensuring strong read consistency.

## Plan

- [x] Update handleGet in cmd/server/handlers.go to accept nodeID and peerTemplate parameters
- [x] Add consistency=strong query parameter check in handleGet
- [x] Redirect to leader using forwardToLeader() when consistency=strong and node is not leader
- [x] Update handler registration in cmd/server/main.go to pass required parameters to handleGet
