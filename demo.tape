Output demo.gif

Set Shell bash
Set FontSize 14
Set Width 1280
Set Height 720
Set Padding 10
Set PlaybackSpeed 0.75

Type "./twui --demo"
Enter
Sleep 2s
Screenshot shot01.png

# Watch List: navigate
Down@250ms 2
Sleep 1s
Up@250ms 1
Sleep 1s

# Favorite the selected channel
Type "f"
Sleep 1500ms
Screenshot shot02.png

# Help overlay
Type "?"
Sleep 2500ms
Screenshot shot03.png
Escape
Sleep 600ms

# Quality picker
Type "i"
Sleep 1500ms
Screenshot shot04.png
Down@200ms 2
Sleep 800ms
Escape
Sleep 600ms

# Theme picker — browse themes, pick one
Type "t"
Sleep 1s
Screenshot shot05.png
Down@350ms 2
Sleep 800ms
Enter
Sleep 1500ms
Screenshot shot06.png

# Switch to Browse view
Tab
Sleep 2500ms
Screenshot shot07.png

# Switch to Search view
Tab
Sleep 800ms
Type "/"
Sleep 400ms
Type "minecraft"
Sleep 1500ms
Screenshot shot08.png
Escape
Sleep 600ms

# Back to Watch List (Search → Ignored → Watch List takes two Tabs)
Tab 2
Sleep 1s

# Reveal chat pane up front so the launched stream's chat is visible
Shift+C
Sleep 500ms

# Launch the selected stream (demo mode: Waiting → Playing + scripted chat)
Enter
Sleep 4s
Screenshot shot09.png

# Let a few more chat messages scroll by
Sleep 2s

# Done
Type "q"
Sleep 500ms
