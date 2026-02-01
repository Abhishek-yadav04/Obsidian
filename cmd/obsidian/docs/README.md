# Architecture Diagrams

This directory contains the architecture diagrams for Obsidian Sentinel WAF v2.1.0.

## Files

- `architecture.puml` - PlantUML source code for the enterprise architecture diagram
- `architecture.svg` - Rendered SVG diagram (auto-generated from PlantUML)

## Regenerating the Diagram

### Prerequisites

Install PlantUML:

```bash
# Using npm (recommended)
npm install -g @plantuml/plantuml

# Or using Java directly
# Download plantuml.jar from https://plantuml.com/download
```

### Generate SVG from PlantUML

```bash
# From the docs directory
cd cmd/obsidian/docs

# Generate SVG
plantuml architecture.puml

# Or using Java directly
java -jar plantuml.jar architecture.puml
```

### Generate PNG (Alternative)

```bash
# Generate PNG instead of SVG
plantuml -tpng architecture.puml

# Or specify output format
plantuml architecture.puml -o ../docs/
```

## GitHub Actions (Optional)

To automatically regenerate the SVG on every push, add this GitHub Action:

```yaml
# .github/workflows/render-diagrams.yml
name: Render Architecture Diagrams

on:
  push:
    paths:
      - 'cmd/obsidian/docs/architecture.puml'
    branches: [ main, v2.0 ]

jobs:
  render:
    runs-on: ubuntu-latest

    steps:
    - uses: actions/checkout@v4

    - name: Setup PlantUML
      run: |
        sudo apt-get update
        sudo apt-get install -y plantuml

    - name: Render Diagrams
      run: |
        cd cmd/obsidian/docs
        plantuml architecture.puml

    - name: Commit Changes
      run: |
        git config --local user.email "action@github.com"
        git config --local user.name "GitHub Action"
        git add cmd/obsidian/docs/architecture.svg
        git commit -m "docs: regenerate architecture diagram" || echo "No changes to commit"
        git push
```

## Diagram Features

The architecture diagram showcases:

- **Real Implementation Details**: No mock data - shows actual metrics (2041 threats, 59 rules, etc.)
- **Enterprise Architecture**: PostgreSQL, Redis clustering, multi-platform webhooks
- **Security Pipeline**: 8-layer request processing with zero-allocation hot path
- **Performance Characteristics**: Concurrent-safe design, sub-millisecond latency
- **Production Ready**: Kubernetes-ready, Prometheus metrics, health checks

## Troubleshooting

### Diagram Not Rendering on GitHub

If the SVG doesn't display properly:

1. **Check File Size**: Ensure SVG is under 100MB
2. **Validate SVG**: Use an online SVG validator
3. **GitHub Cache**: Wait 5-10 minutes for GitHub to process the file
4. **Alternative**: Use PNG format instead of SVG

### PlantUML Issues

- **Java Version**: Ensure Java 8+ is installed
- **Memory**: For large diagrams, increase Java heap: `java -Xmx1024m -jar plantuml.jar`
- **Graphviz**: Install Graphviz for complex diagrams: `sudo apt-get install graphviz`

## Contributing

When updating the architecture:

1. Edit `architecture.puml` with your changes
2. Regenerate the SVG using the commands above
3. Commit both files together
4. Test that the diagram renders correctly on GitHub

The diagram should accurately reflect the current implementation without marketing fluff or mock data.