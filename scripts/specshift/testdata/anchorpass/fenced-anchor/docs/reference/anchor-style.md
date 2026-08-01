# Anchor style

A heading declares an anchor of its own by writing the kramdown attribute
on the line below it:

```markdown
### 28.5.9 CH-EXAMPLE
{: #2859-ch-fenced-example }
```

The attribute the reduction retired was written the same way:

```markdown
#### 15.4.3 Message Format
{: #1543-message-format }
```

Neither block declares an anchor, so the same-page link
[the message format](#1543-message-format) is a reference into a retired
anchor rather than a link this page still resolves.
