@{
    Repository   = "Sen62455/PolyFleet"
    Architecture = "amd64"
    Nodes        = @(
        @{
            Name       = "control-node"
            Target     = "root@CONTROL_NODE_HOST"
            Port       = 22
            Components = @("server", "agent")
        }
        @{
            Name       = "edge-node-a"
            Target     = "root@EDGE_NODE_A_HOST"
            Port       = 22
            Components = @("agent")
        }
        @{
            Name       = "edge-node-b"
            Target     = "root@EDGE_NODE_B_HOST"
            Port       = 22
            Components = @("agent")
        }
    )
}
