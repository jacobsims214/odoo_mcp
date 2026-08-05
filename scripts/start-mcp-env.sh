#!/bin/bash
# Start port-forward to MCP for local development
echo "Starting MCP port-forward on :8088..."
nohup kubectl port-forward -n odoo-mcp svc/odoo-mcp-envoy 8088:8088 > /tmp/mcp-port-forward.log 2>&1 &
PID=$!
echo "MCP port-forward PID: $PID"
echo "MCP available at: http://localhost:8088/mcp"
echo "Dex available at: http://localhost:8088/dex"
echo ""
echo "To stop: kill $PID"