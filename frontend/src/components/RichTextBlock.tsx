import { EditorContent, useEditor } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { useEffect } from 'react';

interface RichTextBlockProps {
  value: unknown;
  readOnly: boolean;
  onChange: (doc: unknown) => void;
}

export function RichTextBlock({ value, readOnly, onChange }: RichTextBlockProps) {
  const editor = useEditor({
    extensions: [StarterKit],
    content: value || { type: 'doc', content: [{ type: 'paragraph' }] },
    editable: !readOnly,
    onBlur: ({ editor }) => onChange(editor.getJSON())
  });

  useEffect(() => {
    editor?.setEditable(!readOnly);
  }, [editor, readOnly]);

  if (!editor) return null;
  return <EditorContent editor={editor} className="rich-editor" />;
}

