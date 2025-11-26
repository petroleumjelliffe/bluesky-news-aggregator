# Claude Instructions for Bluesky News Aggregator

## Session Start Checklist

When starting a new session on this project:

1. **ALWAYS read PROJECT_CONTEXT.md first** - This document contains:
   - Complete function catalog for the entire codebase
   - Database schema reference
   - API endpoints and workflows
   - Architecture overview
   - Recent changes and known issues

2. **Check the specific section relevant to your task**:
   - Adding features? → Read Section 8 (Workflows) and Section 4 (Function Catalog)
   - Fixing bugs? → Read Section 9 (Key Files) and Section 12 (Known Issues)
   - Modifying API? → Read Section 6 (API Endpoints)
   - Changing DB? → Read Section 3 (Database Schema)
   - Adding commands? → Read Section 5 (Command Binaries)

3. **After completing any feature work**:
   - Update PROJECT_CONTEXT.md Section 4 if you added new functions
   - Update Section 12 (Recent Changes) with what you modified
   - Update Section 13 if you changed workflows
   - Update the "Last Updated" date at the top

## Why This Matters

PROJECT_CONTEXT.md eliminates the need to explore the entire codebase every session. With a complete function catalog, you can:
- Jump directly to relevant code
- Understand system architecture quickly
- Avoid breaking existing patterns
- Keep documentation synchronized with code

## Quick Reference

- **Function Catalog**: Section 4
- **Database Schema**: Section 3
- **API Endpoints**: Section 6
- **Workflows**: Section 8
- **Common Tasks**: Section 9

---

**Remember**: Read PROJECT_CONTEXT.md → Work → Update PROJECT_CONTEXT.md
