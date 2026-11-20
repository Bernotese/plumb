# AI clients (MCP)

`plumb mcp` lets an AI client (for example Claude) read Plumb and propose changes, through the
Model Context Protocol over stdio. It uses the public API with an API token. You approve every
change in the web UI.

## Set it up

1. In **Settings → API tokens**, make a token with the tier **plan**. Plumb shows it once. Save it
   in a file that only you can read: `chmod 600 ~/.config/plumb/token`.
2. Copy the CA of Plumb, on the Proxmox host:
   `pct pull <plumb-vmid> /var/lib/plumb/link/ca.crt ~/.config/plumb/ca.crt`.
3. Add Plumb to the client's MCP configuration:

   ```json
   {
     "mcpServers": {
       "plumb": {
         "command": "plumb",
         "args": [
           "mcp",
           "--url",
           "https://<plumb-address>:8443",
           "--token-file",
           "/home/<you>/.config/plumb/token",
           "--ca",
           "/home/<you>/.config/plumb/ca.crt"
         ]
       }
     }
   }
   ```

## What the client may do

What the client **can** do (and nothing else):

- read Targets, Issues (with the difference and the comments), Plans (with their changes and
  documents), Runs, revisions and their difference, Blocks, Images, the starter catalogue, and the
  events since a given point;
- propose a change of Targets, Blocks, Images or ignore rules (a Plan);
- Revert or Accept an Issue (a Plan), re-enroll a Target (a Plan), and drop a Plan;
- comment on an Issue.

What it can **never** do: approve a Plan, ignore drift once or use the Issue action "ignore by
rule", or touch secrets, tokens, users, settings, backups, restore, export or import; nor update
an agent or build or refresh an Image.

A Plan that it proposes may still add or change an ignore rule, or change the guest backup setting
of a Target (its schedule or how many to keep). That is safe only because the client cannot approve
its own Plan, and the risky parts need a person: a rule change always, and a lower keep is
destructive.

The Plan shows the client's reason below the changes, marked "written by API token". A Plan that
takes a value a guest reported, or that removes a firewall, needs a person in any case. A token
may make 30 Plans and 30 comments per minute; above that Plumb refuses the call and says how
long to wait.

## Explain an Issue

The prompt **explain_issue** (argument: the Issue id) gives the client one Issue with what it
needs: the difference, the Target and its Blocks, the source of the resource, the last 5 Runs of
the Target and the last 20 comments, oldest first (when comments must be cut, those of people and
of Plumb are kept before those of tokens).
The client then explains the Issue in a comment and may propose **one** Revert or Accept of this
Issue. You approve it in the web UI, as every Plan.

- Use a token with the tier **plan**. A **read** token gets an error for this prompt.
- Everything that comes from a guest or a person (reported values, comments, Run output, Block
  text, parameters) is marked as untrusted data in the prompt. Plumb tells the client never to
  follow instructions found in it, but to report them in its comment. If a comment of the client
  says that it found such instructions, look at who wrote them.
- The prompt holds no secret: a sensitive value shows only that it differs.
- Long texts and long lists are cut, and the prompt says where and how much. The client then has a
  partial view. An Issue too big for the prompt shows only its id and no action: read it in Plumb.
