#!/bin/bash
# Quick script to run the news classifier

set -e

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Bluesky News Classifier ===${NC}\n"

# Check for OpenAI API key
if [ -z "$OPENAI_API_KEY" ]; then
    echo -e "${YELLOW}⚠ OPENAI_API_KEY not set${NC}"
    echo "Please export your OpenAI API key:"
    echo "  export OPENAI_API_KEY='sk-...'"
    echo ""
    echo "Or run in display-only mode to view existing stories:"
    echo "  ./scripts/classify-news.sh --display-only"
    exit 1
fi

# Default values
LIMIT=20
THRESHOLD=0.80
MIN_SHARES=2
MIGRATE=false
DISPLAY_ONLY=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --limit)
            LIMIT="$2"
            shift 2
            ;;
        --threshold)
            THRESHOLD="$2"
            shift 2
            ;;
        --min-shares)
            MIN_SHARES="$2"
            shift 2
            ;;
        --migrate)
            MIGRATE=true
            shift
            ;;
        --display-only)
            DISPLAY_ONLY=true
            shift
            ;;
        --help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --limit N          Number of links to classify (default: 20)"
            echo "  --threshold F      Similarity threshold 0-1 (default: 0.80)"
            echo "  --min-shares N     Minimum shares required (default: 2)"
            echo "  --migrate          Run database migration first"
            echo "  --display-only     Only display existing stories"
            echo "  --help             Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                                    # Classify 20 recent links"
            echo "  $0 --limit 50 --threshold 0.85       # Custom parameters"
            echo "  $0 --display-only                    # View existing stories"
            echo "  $0 --migrate                         # Run migration first"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Build flags
FLAGS="-limit $LIMIT -threshold $THRESHOLD -min-shares $MIN_SHARES"

if [ "$MIGRATE" = true ]; then
    FLAGS="$FLAGS -migrate"
fi

if [ "$DISPLAY_ONLY" = true ]; then
    FLAGS="$FLAGS -display-only"
fi

echo -e "${GREEN}Running classifier with:${NC}"
echo "  Limit: $LIMIT links"
echo "  Threshold: $THRESHOLD"
echo "  Min shares: $MIN_SHARES"
[ "$MIGRATE" = true ] && echo "  Running migration: yes"
[ "$DISPLAY_ONLY" = true ] && echo "  Display only: yes"
echo ""

# Run the classifier
go run ./cmd/classify $FLAGS
