// {{name}}: a Persona extension (docs/extensions/extensions.md "Writing extensions: SDK, templates and command line"). A page on the main navigation with a
// list you can search and open, and a card on the overview. Replace the sample items with your service's.
//
// Build into one executable (the package holds only that): personad extension pack.
import { main } from "../sdk/ts/src/index.ts";
import { program } from "./app.ts";

main(program());
